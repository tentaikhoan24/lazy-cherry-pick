package git

import (
	"context"
)

type CherryPickArgs struct {
	Target   string   `json:"target"`
	Shas     []string `json:"shas"`
	Strategy string   `json:"strategy"` // "smart" (default), "theirs"/"incoming", "ours"
	// Messages maps a full SHA → replacement commit message (M11b edit-message).
	// After a commit is picked, if its SHA has a non-empty entry the commit is
	// amended (`git commit --amend -m`). Absent/empty → keep the original message.
	Messages map[string]string `json:"messages"`
	// OnProgress is called after each successful commit with 1-based index and
	// total count. Not serialised — injected by the RPC handler in main.go.
	OnProgress func(n, total int, sha string) `json:"-"`
}

// CherryPick applies the given commits onto Target sequentially. If Target
// differs from the current branch, the working tree must be clean — otherwise
// returns CodeDirtyTree. On the first conflicting commit, the cherry-pick is
// aborted (repo returned to clean state) and CodeCherryPickConflict is returned
// with the partial result attached in Data.
//
// Future (M5): switch from abort-on-conflict to leave-in-conflict-state so the
// frontend can drive a 3-way merge resolver.
func (r *Repo) CherryPick(ctx context.Context, args CherryPickArgs) (*CherryPickResult, error) {
	current, detached, err := r.CurrentBranch(ctx)
	if err != nil {
		return nil, err
	}

	// Resolve final target. If caller provided one, use it — even when HEAD is
	// detached the checkout below will fix the detached state. We only fail when
	// detached AND no target was given (nowhere to switch to).
	target := args.Target
	if target == "" {
		if detached {
			return nil, &Error{
				Code:    CodeGitCommandFailed,
				Message: "cannot cherry-pick onto detached HEAD; pick a target branch in the dropdown",
			}
		}
		target = current
	}

	// Cherry-pick requires a clean working tree both for switching target
	// (otherwise checkout would overwrite changes) and for the picks themselves
	// (otherwise git refuses or produces misleading "conflict" errors).
	dirty, err := r.IsDirty(ctx)
	if err != nil {
		return nil, err
	}
	if dirty {
		return nil, &Error{
			Code:    CodeDirtyTree,
			Message: "working tree has uncommitted changes; commit, stash, or discard before cherry-pick",
			Data: map[string]any{
				"current": current,
				"target":  target,
			},
		}
	}

	if target != current {
		if _, err := run(ctx, r.Path, "checkout", target); err != nil {
			return nil, err
		}
	}

	result := &CherryPickResult{
		Applied:   []string{},
		Skipped:   []string{},
		Conflicts: []ConflictInfo{},
	}

	for _, sha := range args.Shas {
		gitArgs := []string{"cherry-pick"}
		switch args.Strategy {
		case "theirs", "incoming":
			gitArgs = append(gitArgs, "--strategy-option=theirs")
		case "ours":
			gitArgs = append(gitArgs, "--strategy-option=ours")
		}
		gitArgs = append(gitArgs, sha)

		if _, err := run(ctx, r.Path, gitArgs...); err != nil {
			// Use the public ConflictFiles (git status --porcelain) — more reliable than
			// git diff --diff-filter=U, especially for AA (added-both-sides) conflicts.
			var files []string
			if cfr, err2 := r.ConflictFiles(ctx, ConflictFilesArgs{}); err2 == nil && cfr != nil {
				for _, f := range cfr.Files {
					files = append(files, f.Path)
				}
			}
			if len(files) == 0 {
				// Commit is already applied or produces an empty diff — skip it and continue.
				run(ctx, r.Path, "cherry-pick", "--abort") //nolint:errcheck
				result.Skipped = append(result.Skipped, sha)
				continue
			}
			result.Conflicts = append(result.Conflicts, ConflictInfo{Sha: sha, Files: files})
			// Leave repo in conflict state — frontend drives resolution via ConflictResolver.
			return result, &Error{
				Code:    CodeCherryPickConflict,
				Message: "cherry-pick produced conflicts on " + sha,
				Data: map[string]any{
					"applied":   result.Applied,
					"conflicts": result.Conflicts,
				},
			}
		}
		// M11b — apply a per-commit message override by amending the just-made commit.
		if msg := args.Messages[sha]; msg != "" {
			if _, err := run(ctx, r.Path, "commit", "--amend", "-m", msg); err != nil {
				return result, err
			}
		}
		result.Applied = append(result.Applied, sha)
		if args.OnProgress != nil {
			args.OnProgress(len(result.Applied), len(args.Shas), sha)
		}
	}
	return result, nil
}

