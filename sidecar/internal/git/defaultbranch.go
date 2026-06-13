package git

import (
	"context"
	"strings"
)

type DefaultBranchArgs struct {
	Remote string `json:"remote"` // defaults to "origin" if empty
}

// DefaultBranch returns the repo's default branch — the branch most other
// branches were created from. Used as the suggested base when creating a PR.
//
// Strategy (each fallback fires only on error):
//  1. `git symbolic-ref --short refs/remotes/<remote>/HEAD` → "origin/main" → strip "origin/" → "main"
//     This is set by `git clone` and updated by `git remote set-head <remote> --auto`.
//  2. Common conventional names: main, master, develop (first local branch that exists).
//  3. Empty string — caller falls back to whatever they want.
//
// We never guess based on commit ancestry — that's expensive and gets it wrong
// for repos with rebased branches.
func (r *Repo) DefaultBranch(ctx context.Context, args DefaultBranchArgs) (string, error) {
	remote := args.Remote
	if remote == "" {
		remote = "origin"
	}

	// 1. Remote HEAD symbolic-ref — the authoritative source when present
	ref := "refs/remotes/" + remote + "/HEAD"
	if out, err := run(ctx, r.Path, "symbolic-ref", "--short", ref); err == nil {
		name := strings.TrimSpace(string(out))
		// "origin/main" → "main"
		if prefix := remote + "/"; strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
		}
		if name != "" {
			return name, nil
		}
	}

	// 2. Conventional fallback names
	for _, candidate := range []string{"main", "master", "develop"} {
		if _, err := run(ctx, r.Path, "rev-parse", "--verify", "--quiet", "refs/heads/"+candidate); err == nil {
			return candidate, nil
		}
	}

	// 3. Nothing matched — caller decides
	return "", nil
}
