package git

import (
	"context"
	"fmt"
	"strings"
)

type CherryArgs struct {
	Target   string `json:"target"`
	Source   string `json:"source"`
	MaxCount int    `json:"maxCount"` // limit to last N commits; 0 = no limit
}

// Cherry runs `git cherry <target> <source> [limit]` and returns the SHAs of
// commits in Source whose equivalent patch already exists in Target (marked `-`).
// When MaxCount > 0, only the last MaxCount commits are compared — matching the
// visible commit list window and keeping the call fast on large repos.
// Returns an empty slice on any error (best-effort; caller treats as "unknown").
func (r *Repo) Cherry(ctx context.Context, args CherryArgs) ([]string, error) {
	gitArgs := []string{"cherry", args.Target, args.Source}
	if args.MaxCount > 0 {
		// `git cherry <upstream> <head> <limit>` stops at <limit>.
		// <source>~N may not exist if the branch has fewer commits; run()
		// returns an error in that case and we fall through to return [].
		gitArgs = append(gitArgs, fmt.Sprintf("%s~%d", args.Source, args.MaxCount))
	}
	out, err := run(ctx, r.Path, gitArgs...)
	if err != nil && args.MaxCount > 0 {
		// <source>~N doesn't exist when branch has fewer commits than MaxCount; retry without limit.
		out, err = run(ctx, r.Path, "cherry", args.Target, args.Source)
	}
	if err != nil {
		return []string{}, nil
	}
	var applied []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "- ") {
			sha := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			if sha != "" {
				applied = append(applied, sha)
			}
		}
	}
	if applied == nil {
		return []string{}, nil
	}
	return applied, nil
}
