package git

import (
	"context"
	"strconv"
)

type CompareArgs struct {
	Base  string `json:"base"`  // the branch/ref to merge into
	Head  string `json:"head"`  // the branch/ref carrying the changes
	Limit int    `json:"limit"` // cap the number of commits returned; 0 = default 100
}

// CompareResult lists the commits present in Head but not in Base — i.e. what a
// PR from Head into Base would propose. Used by the MCP `compare_branches` tool
// so an AI can reason about which commits a cherry-pick / PR would carry.
type CompareResult struct {
	Base    string   `json:"base"`
	Head    string   `json:"head"`
	Ahead   int      `json:"ahead"` // total commits in head..base direction (head ahead of base)
	Commits []Commit `json:"commits"`
}

// CompareBranches returns the commits in `head` that are not in `base`
// (`git log base..head`). The result mirrors the Commit shape from Commits()
// so callers can reuse the same rendering.
func (r *Repo) CompareBranches(ctx context.Context, args CompareArgs) (*CompareResult, error) {
	if args.Base == "" || args.Head == "" {
		return nil, &Error{Code: CodeGitCommandFailed, Message: "both base and head are required"}
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 100
	}

	rangeSpec := args.Base + ".." + args.Head
	gitArgs := []string{
		"log",
		"--format=format:" + commitFormat,
		"-n", strconv.Itoa(limit),
		rangeSpec,
	}
	out, err := run(ctx, r.Path, gitArgs...)
	if err != nil {
		return nil, err
	}

	commits := parseCommits(string(out))
	return &CompareResult{
		Base:    args.Base,
		Head:    args.Head,
		Ahead:   len(commits),
		Commits: commits,
	}, nil
}
