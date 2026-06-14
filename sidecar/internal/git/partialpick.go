package git

import (
	"context"
	"strings"
)

// M11c — partial-file cherry-pick. Apply only a subset of a commit's files
// onto the target branch as a new commit.

type PartialPickArgs struct {
	Target  string   `json:"target"`  // branch to apply onto ("" = current HEAD)
	Sha     string   `json:"sha"`     // source commit
	Keep    []string `json:"keep"`    // files to keep; every other changed file is reverted
	Message string   `json:"message"` // optional; "" → reuse the source commit's message + author (-C)
}

type PartialPickResult struct {
	Sha  string   `json:"sha"`  // new commit SHA
	Kept []string `json:"kept"` // files actually committed
}

// PartialPick applies `git cherry-pick -n <sha>`, reverts every changed file
// not in Keep (via `git restore --source=HEAD`, which also removes added files
// and restores deleted ones), then commits the remainder. On a conflict during
// the no-commit pick it discards everything (`reset --hard`) and returns a
// CodeCherryPickConflict error — partial pick only handles conflict-free
// commits; the caller should fall back to a whole-commit pick to resolve.
func (r *Repo) PartialPick(ctx context.Context, args PartialPickArgs) (*PartialPickResult, error) {
	if args.Sha == "" {
		return nil, &Error{Code: CodeGitCommandFailed, Message: "partial pick: sha is required"}
	}
	if len(args.Keep) == 0 {
		return nil, &Error{Code: CodeGitCommandFailed, Message: "partial pick: select at least one file to keep"}
	}

	current, detached, err := r.CurrentBranch(ctx)
	if err != nil {
		return nil, err
	}
	target := args.Target
	if target == "" {
		if detached {
			return nil, &Error{Code: CodeGitCommandFailed, Message: "cannot partial-pick onto detached HEAD; pick a target branch"}
		}
		target = current
	}

	dirty, err := r.IsDirty(ctx)
	if err != nil {
		return nil, err
	}
	if dirty {
		return nil, &Error{
			Code:    CodeDirtyTree,
			Message: "working tree has uncommitted changes; commit, stash, or discard before partial pick",
		}
	}

	if target != current {
		if _, err := run(ctx, r.Path, "checkout", target); err != nil {
			return nil, err
		}
	}

	// Stage the whole commit without committing.
	if _, err := run(ctx, r.Path, "cherry-pick", "-n", args.Sha); err != nil {
		run(ctx, r.Path, "cherry-pick", "--quit")  //nolint:errcheck — clear any sequencer state
		run(ctx, r.Path, "reset", "--hard", "HEAD") //nolint:errcheck — discard the partial application
		return nil, &Error{
			Code:    CodeCherryPickConflict,
			Message: "partial pick: commit conflicts with the target; pick the whole commit to resolve conflicts",
		}
	}

	// Revert every staged file that the user did NOT select.
	staged, err := run(ctx, r.Path, "diff", "--cached", "--name-only")
	if err != nil {
		run(ctx, r.Path, "reset", "--hard", "HEAD") //nolint:errcheck
		return nil, err
	}
	keepSet := make(map[string]bool, len(args.Keep))
	for _, k := range args.Keep {
		keepSet[k] = true
	}
	var unwanted []string
	for _, f := range strings.Split(strings.TrimSpace(string(staged)), "\n") {
		if f != "" && !keepSet[f] {
			unwanted = append(unwanted, f)
		}
	}
	if len(unwanted) > 0 {
		restoreArgs := append([]string{"restore", "--source=HEAD", "--staged", "--worktree", "--"}, unwanted...)
		if _, err := run(ctx, r.Path, restoreArgs...); err != nil {
			run(ctx, r.Path, "reset", "--hard", "HEAD") //nolint:errcheck
			return nil, err
		}
	}

	// Nothing left to commit → the selected files had no changes in this commit.
	if _, err := run(ctx, r.Path, "diff", "--cached", "--quiet"); err == nil {
		run(ctx, r.Path, "reset", "--hard", "HEAD") //nolint:errcheck
		return nil, &Error{Code: CodeGitCommandFailed, Message: "partial pick: the selected files have no changes in this commit"}
	}

	var commitArgs []string
	if args.Message != "" {
		commitArgs = []string{"commit", "-m", args.Message}
	} else {
		commitArgs = []string{"commit", "-C", args.Sha} // reuse original message + authorship
	}
	if _, err := run(ctx, r.Path, commitArgs...); err != nil {
		return nil, err
	}

	head, err := run(ctx, r.Path, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	return &PartialPickResult{Sha: strings.TrimSpace(string(head)), Kept: args.Keep}, nil
}
