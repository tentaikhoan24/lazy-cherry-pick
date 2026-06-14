package git

import (
	"context"
	"strings"
)

// M11a — auto-stash. The desktop app stashes uncommitted changes before a
// cherry-pick batch (so the existing clean-tree requirement is satisfied) and
// pops them once the whole flow finishes. Lifecycle is orchestrated by the
// frontend because the conflict path spans multiple RPC calls.

type StashArgs struct {
	Message          string `json:"message"`
	IncludeUntracked bool   `json:"includeUntracked"`
}

type StashResult struct {
	Stashed bool `json:"stashed"`
}

// Stash saves uncommitted changes (optionally including untracked files) under
// the given message. Returns Stashed=false when the working tree was already
// clean (git prints "No local changes to save") — not an error.
func (r *Repo) Stash(ctx context.Context, args StashArgs) (*StashResult, error) {
	gitArgs := []string{"stash", "push"}
	if args.IncludeUntracked {
		gitArgs = append(gitArgs, "-u")
	}
	if args.Message != "" {
		gitArgs = append(gitArgs, "-m", args.Message)
	}
	out, err := run(ctx, r.Path, gitArgs...)
	if err != nil {
		return nil, err
	}
	stashed := !strings.Contains(string(out), "No local changes to save")
	return &StashResult{Stashed: stashed}, nil
}

type StashPopArgs struct {
	Message string `json:"message"`
}

type StashPopResult struct {
	Popped bool `json:"popped"`
}

// StashPop restores the most recent stash whose message contains the given
// text (so we only ever pop our own "lcp-autostash" entry, never one the user
// created). With an empty Message it pops stash@{0}. Returns Popped=false when
// no matching entry exists. A pop that conflicts surfaces as an error.
func (r *Repo) StashPop(ctx context.Context, args StashPopArgs) (*StashPopResult, error) {
	ref := "stash@{0}"
	if args.Message != "" {
		out, err := run(ctx, r.Path, "stash", "list")
		if err != nil {
			return nil, err
		}
		found := ""
		for _, line := range strings.Split(string(out), "\n") {
			// format: "stash@{N}: On <branch>: <message>"
			if strings.Contains(line, args.Message) {
				if i := strings.Index(line, ":"); i > 0 {
					found = line[:i]
					break
				}
			}
		}
		if found == "" {
			return &StashPopResult{Popped: false}, nil
		}
		ref = found
	}
	if _, err := run(ctx, r.Path, "stash", "pop", ref); err != nil {
		return nil, err
	}
	return &StashPopResult{Popped: true}, nil
}

// M11b — squash. Collapse every commit added since Base into a single commit.

type SquashArgs struct {
	// Base is the SHA the target branch tip pointed to BEFORE the picks. The
	// caller captures it before the batch (robust across skips/conflicts).
	Base    string `json:"base"`
	Message string `json:"message"`
}

type SquashResult struct {
	Squashed bool `json:"squashed"`
}

// SquashCommits runs `git reset --soft <base>` then `git commit -m <message>`,
// folding all commits added since Base into one. Returns Squashed=false when
// nothing was added (HEAD already at Base / nothing staged) — not an error, so
// the caller can squash unconditionally and get a no-op for a single commit.
func (r *Repo) SquashCommits(ctx context.Context, args SquashArgs) (*SquashResult, error) {
	if args.Base == "" {
		return nil, &Error{Code: CodeGitCommandFailed, Message: "squash: base commit is required"}
	}
	if _, err := run(ctx, r.Path, "reset", "--soft", args.Base); err != nil {
		return nil, err
	}
	// `git diff --cached --quiet` exits 0 (err == nil) when nothing is staged.
	if _, err := run(ctx, r.Path, "diff", "--cached", "--quiet"); err == nil {
		return &SquashResult{Squashed: false}, nil
	}
	if _, err := run(ctx, r.Path, "commit", "-m", args.Message); err != nil {
		return nil, err
	}
	return &SquashResult{Squashed: true}, nil
}
