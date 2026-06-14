package git

import (
	"context"
	"os"
	"path/filepath"
)

// ── DiffTexts ─────────────────────────────────────────────────────────────────

type DiffTextsArgs struct {
	LeftText  string `json:"leftText"`
	RightText string `json:"rightText"`
}

// DiffTexts produces a unified diff between two arbitrary text blobs by writing
// them to temp files and running `git diff --no-index`. Used by the AI-review
// modal to show "[conflict original] vs [AI resolved]" without needing either
// side to be a git ref. Returns the same FileDiffResult.Diff shape the frontend
// FileDiff parser consumes.
//
// Note: `git diff --no-index` exits 1 when the files differ (like grep), which
// run() reports as an error — we treat exit code 1 as "differences found" and
// return the captured stdout. Exit 0 means identical (empty diff).
func (r *Repo) DiffTexts(ctx context.Context, args DiffTextsArgs) (*FileDiffResult, error) {
	tmp, err := os.MkdirTemp("", "lcp-difftext-*")
	if err != nil {
		return nil, &Error{Code: CodeGitCommandFailed, Message: "cannot create temp dir: " + err.Error()}
	}
	defer os.RemoveAll(tmp)

	leftPath := filepath.Join(tmp, "left")
	rightPath := filepath.Join(tmp, "right")
	if werr := os.WriteFile(leftPath, []byte(args.LeftText), 0644); werr != nil {
		return nil, &Error{Code: CodeGitCommandFailed, Message: "cannot write left text: " + werr.Error()}
	}
	if werr := os.WriteFile(rightPath, []byte(args.RightText), 0644); werr != nil {
		return nil, &Error{Code: CodeGitCommandFailed, Message: "cannot write right text: " + werr.Error()}
	}

	// dir="" — no -C; --no-index compares absolute paths, cwd is irrelevant.
	out, err := run(ctx, "", "diff", "--no-index", "--unified=99999", leftPath, rightPath)
	if err != nil {
		if e, ok := err.(*Error); ok {
			if code, _ := e.Data["exitCode"].(int); code == 1 {
				// exit 1 = differences found (normal for --no-index).
				return &FileDiffResult{Diff: string(out)}, nil
			}
		}
		return nil, err
	}
	// exit 0 = identical files, empty diff.
	return &FileDiffResult{Diff: string(out)}, nil
}
