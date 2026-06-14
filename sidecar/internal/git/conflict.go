package git

import (
	"context"
	"strings"
)

// ── conflict file listing ─────────────────────────────────────────────────────

type ConflictFilesArgs struct{}

type ConflictFileInfo struct {
	Path   string `json:"path"`
	Status string `json:"status"` // UU, AA, DD, AU, UA, DU, UD
}

type ConflictFilesResult struct {
	Files []ConflictFileInfo `json:"files"`
}

// ConflictFiles returns the list of files with merge conflicts in the working tree.
func (r *Repo) ConflictFiles(ctx context.Context, _ ConflictFilesArgs) (*ConflictFilesResult, error) {
	out, err := run(ctx, r.Path, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	files := []ConflictFileInfo{}
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		xy := line[:2]
		path := strings.TrimSpace(line[3:])
		if conflictXY(xy) {
			files = append(files, ConflictFileInfo{Path: path, Status: xy})
		}
	}
	return &ConflictFilesResult{Files: files}, nil
}

func conflictXY(xy string) bool {
	switch xy {
	case "UU", "AA", "DD", "AU", "UA", "DU", "UD":
		return true
	}
	return false
}

// ── resolve a single file ─────────────────────────────────────────────────────

type ResolveConflictArgs struct {
	File     string `json:"file"`
	Strategy string `json:"strategy"` // "ours" or "theirs"
}

// ResolveConflict accepts one side of a conflict and stages the file.
func (r *Repo) ResolveConflict(ctx context.Context, args ResolveConflictArgs) (any, error) {
	if args.Strategy != "ours" && args.Strategy != "theirs" {
		return nil, &Error{Code: CodeGitCommandFailed, Message: "strategy must be \"ours\" or \"theirs\""}
	}
	if _, err := run(ctx, r.Path, "checkout", "--"+args.Strategy, "--", args.File); err != nil {
		return nil, err
	}
	if _, err := run(ctx, r.Path, "add", "--", args.File); err != nil {
		return nil, err
	}
	return map[string]any{"resolved": true}, nil
}

// ── restore conflict markers ──────────────────────────────────────────────────

type RestoreConflictArgs struct {
	File string `json:"file"`
}

// RestoreConflict re-creates the conflict markers for a file by running
// `git checkout -m -- <file>`, rebuilding the merged working-tree content from
// the index stages (:1 base, :2 ours, :3 theirs). Used to undo an AI-proposed
// resolution that the user discards. Works only while the file is still
// unmerged in the index — which holds because the AI resolver is blocked from
// running `git add` (Bash disallowed).
func (r *Repo) RestoreConflict(ctx context.Context, args RestoreConflictArgs) (any, error) {
	if _, err := run(ctx, r.Path, "checkout", "-m", "--", args.File); err != nil {
		return nil, err
	}
	return map[string]any{"restored": true}, nil
}

// ── continue cherry-pick ──────────────────────────────────────────────────────

type ContinueCherryArgs struct {
	// Optional replacement message for the just-resolved commit (M11b edit-message).
	// Empty → keep the original message.
	Message string `json:"message"`
}

type ContinueCherryResult struct {
	Done bool `json:"done"`
}

// ContinueCherry runs `git cherry-pick --continue --no-edit` to resume after
// conflicts are resolved. --no-edit skips the editor prompt entirely.
// If the resolved commit turns out to be empty (e.g. the conflict was resolved
// by accepting the target side), we skip it with --skip instead of failing.
// A non-empty args.Message amends the resumed commit with that message.
func (r *Repo) ContinueCherry(ctx context.Context, args ContinueCherryArgs) (*ContinueCherryResult, error) {
	_, err := run(ctx, r.Path, "cherry-pick", "--continue", "--no-edit")
	if err != nil {
		if e, ok := err.(*Error); ok && (strings.Contains(e.Message, "now empty") || strings.Contains(e.Message, "cherry-pick --skip")) {
			if _, skipErr := run(ctx, r.Path, "cherry-pick", "--skip"); skipErr == nil {
				// Commit was skipped — no commit to amend, so ignore any message.
				return &ContinueCherryResult{Done: true}, nil
			}
		}
		return nil, err
	}
	if args.Message != "" {
		if _, err := run(ctx, r.Path, "commit", "--amend", "-m", args.Message); err != nil {
			return nil, err
		}
	}
	return &ContinueCherryResult{Done: true}, nil
}
