package git

import (
	"context"
	"strings"
)

type FetchArgs struct {
	Remote string `json:"remote"` // defaults to "origin"
}

type FetchResult struct {
	Remote string `json:"remote"`
}

// Fetch runs git fetch --prune <remote>, updating all remote-tracking refs
// without modifying any local branch.
func (r *Repo) Fetch(ctx context.Context, args FetchArgs) (*FetchResult, error) {
	remote := args.Remote
	if remote == "" {
		remote = "origin"
	}
	if _, err := run(ctx, r.Path, "fetch", "--prune", remote); err != nil {
		return nil, err
	}
	return &FetchResult{Remote: remote}, nil
}

type PullArgs struct {
	Branch string `json:"branch"` // local branch to fast-forward
	Remote string `json:"remote"` // defaults to "origin"
}

type PullResult struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
}

// Pull fast-forwards a local branch from its remote counterpart without
// requiring a checkout. Uses "git fetch <remote> <branch>:<branch>" which
// only succeeds when the update is a fast-forward; non-fast-forward refs are
// rejected safely.
//
// If `Branch` is the currently checked-out branch, that refspec form is
// refused by git ("refusing to fetch into branch ... checked out") because it
// would move the branch ref without touching the index/working tree. In that
// case, fetch the branch then fast-forward-merge it into the checkout instead.
func (r *Repo) Pull(ctx context.Context, args PullArgs) (*PullResult, error) {
	remote := args.Remote
	if remote == "" {
		remote = "origin"
	}

	if headOut, err := run(ctx, r.Path, "symbolic-ref", "--short", "-q", "HEAD"); err == nil && strings.TrimSpace(string(headOut)) == args.Branch {
		if _, err := run(ctx, r.Path, "fetch", remote, args.Branch); err != nil {
			return nil, err
		}
		if _, err := run(ctx, r.Path, "merge", "--ff-only", "FETCH_HEAD"); err != nil {
			return nil, err
		}
		return &PullResult{Remote: remote, Branch: args.Branch}, nil
	}

	refspec := args.Branch + ":" + args.Branch
	if _, err := run(ctx, r.Path, "fetch", remote, refspec); err != nil {
		return nil, err
	}
	return &PullResult{Remote: remote, Branch: args.Branch}, nil
}
