package git

import (
	"context"
	"strconv"
	"strings"
)

// CommitsArgs is the input to Commits. Limit defaults to 100 if <= 0.
// Ref defaults to HEAD if empty.
type CommitsArgs struct {
	Ref    string       `json:"ref"`
	Limit  int          `json:"limit"`
	Skip   int          `json:"skip"`
	Filter CommitFilter `json:"filter"`
}

// commitFormat splits fields with NUL (0x00) and terminates each commit record
// with RS (0x1e). %s is the subject (single line, no embedded newlines).
const commitFormat = "%H%x00%P%x00%an%x00%ae%x00%at%x00%s%x00%D%x1e"

// Commits runs `git log` with NUL-separated fields and parses the output.
// The Filter fields map directly to git-log flags so filtering happens server-side.
func (r *Repo) Commits(ctx context.Context, args CommitsArgs) ([]Commit, error) {
	if args.Ref == "" {
		args.Ref = "HEAD"
	}
	if args.Limit <= 0 {
		args.Limit = 100
	}

	gitArgs := []string{
		"log",
		"--format=format:" + commitFormat,
		"-n", strconv.Itoa(args.Limit),
	}
	if args.Skip > 0 {
		gitArgs = append(gitArgs, "--skip", strconv.Itoa(args.Skip))
	}
	f := args.Filter
	if f.Author != "" {
		gitArgs = append(gitArgs, "--author="+f.Author)
	}
	if f.MessageContains != "" {
		gitArgs = append(gitArgs, "--fixed-strings", "--grep="+f.MessageContains)
	}
	if f.Since != "" {
		gitArgs = append(gitArgs, "--since="+f.Since)
	}
	if f.Until != "" {
		gitArgs = append(gitArgs, "--until="+f.Until)
	}
	gitArgs = append(gitArgs, args.Ref)
	if f.PathGlob != "" {
		gitArgs = append(gitArgs, "--", f.PathGlob)
	}

	out, err := run(ctx, r.Path, gitArgs...)
	if err != nil {
		// `git log` on an empty repo (no commits yet) returns non-zero; treat as empty.
		if strings.Contains(string(out), "") && isEmptyRepoErr(err) {
			return []Commit{}, nil
		}
		return nil, err
	}

	return parseCommits(string(out)), nil
}

// parseCommits decodes the NUL/RS-delimited output of a `git log` invocation
// using commitFormat. Shared by Commits() and CompareBranches().
func parseCommits(out string) []Commit {
	records := strings.Split(out, "\x1e")
	commits := make([]Commit, 0, len(records))
	for _, raw := range records {
		raw = strings.TrimLeft(raw, "\n")
		if raw == "" {
			continue
		}
		fields := strings.Split(raw, "\x00")
		if len(fields) < 7 {
			continue
		}
		ts, _ := strconv.ParseInt(fields[4], 10, 64)
		c := Commit{
			Sha:     fields[0],
			Author:  fields[2],
			Email:   fields[3],
			Time:    ts,
			Subject: fields[5],
		}
		if fields[1] != "" {
			c.Parents = strings.Fields(fields[1])
		}
		if fields[6] != "" {
			for _, ref := range strings.Split(fields[6], ", ") {
				ref = strings.TrimSpace(ref)
				if ref != "" {
					c.Refs = append(c.Refs, ref)
				}
			}
		}
		commits = append(commits, c)
	}
	return commits
}

func isEmptyRepoErr(err error) bool {
	e, ok := err.(*Error)
	if !ok {
		return false
	}
	stderr, _ := e.Data["stderr"].(string)
	return strings.Contains(stderr, "does not have any commits yet") ||
		strings.Contains(stderr, "unknown revision") ||
		strings.Contains(stderr, "bad default revision")
}
