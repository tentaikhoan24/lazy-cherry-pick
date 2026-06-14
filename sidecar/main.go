// Sidecar JSON-RPC 2.0 server over stdin/stdout (newline-delimited).
//
// Wire protocol: see docs/IPC.md.
// Architecture:
//   - internal/rpc — generic NDJSON JSON-RPC server (transport)
//   - internal/git — git operations (shells out to `git` CLI)
//   - this file   — registers handlers, glues git layer to rpc layer
//
// Add a new method by:
//  1. Implementing it in internal/git/ (or wherever appropriate)
//  2. Registering a handler below with rpcSrv.Register("name", ...)
//  3. Documenting it in docs/IPC.md
//
// See CLAUDE.md hard rules for the operational invariants.
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lazy-cherry-pick/sidecar/internal/git"
	"github.com/lazy-cherry-pick/sidecar/internal/mcp"
	"github.com/lazy-cherry-pick/sidecar/internal/rpc"
)

const sidecarVersion = "0.2.0"

func main() {
	// MCP server mode — started by an AI client (Claude Desktop / Cursor / VS
	// Code) with `--mcp`. This is a completely separate code path from the
	// NDJSON RPC mode the Tauri app uses; the app never passes this flag, so
	// existing behaviour is unaffected. See internal/mcp.
	if len(os.Args) > 1 && os.Args[1] == "--mcp" {
		defaultRepo := os.Getenv("LCP_DEFAULT_REPO")
		srv := mcp.NewServer(defaultRepo)
		if err := srv.Serve(context.Background(), os.Stdin, os.Stdout, os.Stderr); err != nil {
			os.Exit(1)
		}
		return
	}

	srv := rpc.NewServer()
	registerHandlers(srv)
	if err := srv.Serve(context.Background(), os.Stdin, os.Stdout, os.Stderr); err != nil {
		os.Exit(1)
	}
}

func registerHandlers(s *rpc.Server) {
	// Liveness / debugging
	s.Register("ping", func(ctx context.Context, _ json.RawMessage) (any, *rpc.Error) {
		return "pong", nil
	})
	s.Register("version", func(ctx context.Context, _ json.RawMessage) (any, *rpc.Error) {
		return map[string]string{
			"sidecar": sidecarVersion,
			"go":      runtime.Version(),
			"git":     gitVersion(),
		}, nil
	})

	// Git operations (M2)
	s.Register("git.openRepo", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		current, detached, err := r.CurrentBranch(ctx)
		if err != nil {
			return nil, err
		}
		cpHead := ""
		if data, err2 := os.ReadFile(filepath.Join(r.Path, ".git", "CHERRY_PICK_HEAD")); err2 == nil {
			cpHead = strings.TrimSpace(string(data))
		}
		return map[string]any{
			"path":           r.Path,
			"branch":         current,
			"detached":       detached,
			"cherryPickHead": cpHead,
		}, nil
	}))

	s.Register("git.status", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.Status(ctx)
	}))

	s.Register("git.branches", wrap1(func(ctx context.Context, p struct {
		Repo          string `json:"repo"`
		IncludeRemote bool   `json:"includeRemote"`
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.Branches(ctx, p.IncludeRemote)
	}))

	s.Register("git.commits", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.CommitsArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.Commits(ctx, p.CommitsArgs)
	}))

	s.Register("git.remotes", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.Remotes(ctx, struct{}{})
	}))

	s.Register("git.defaultBranch", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.DefaultBranchArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.DefaultBranch(ctx, p.DefaultBranchArgs)
	}))

	// git.cherryPick uses a manual handler (not wrap1) so it can inject the
	// progress callback that streams per-commit notifications to the frontend.
	s.Register("git.cherryPick", func(ctx context.Context, raw json.RawMessage) (any, *rpc.Error) {
		var p struct {
			Repo string `json:"repo"`
			git.CherryPickArgs
		}
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, &rpc.Error{Code: -32602, Message: "Invalid params: " + err.Error()}
			}
		}
		p.OnProgress = func(n, total int, sha string) {
			_ = rpc.WriteProgress(ctx, map[string]any{
				"n":     n,
				"total": total,
				"sha":   sha,
			})
		}
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, toRPCError(err)
		}
		result, err := r.CherryPick(ctx, p.CherryPickArgs)
		if err != nil {
			return nil, toRPCError(err)
		}
		return result, nil
	})

	s.Register("git.abort", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return nil, r.Abort(ctx)
	}))

	s.Register("git.createBranch", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.CreateBranchArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.CreateBranch(ctx, p.CreateBranchArgs)
	}))

	s.Register("git.fetch", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.FetchArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.Fetch(ctx, p.FetchArgs)
	}))

	s.Register("git.pull", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.PullArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.Pull(ctx, p.PullArgs)
	}))

	s.Register("git.push", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.PushArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.Push(ctx, p.PushArgs)
	}))

	// M5 methods
	s.Register("git.commitDetail", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.CommitDetailArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.CommitDetail(ctx, p.CommitDetailArgs)
	}))

	s.Register("git.commitFiles", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.CommitFilesArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.CommitFiles(ctx, p.CommitFilesArgs)
	}))

	s.Register("git.dryRunPick", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.DryRunArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.DryRunPick(ctx, p.DryRunArgs)
	}))

	s.Register("git.fileDiff", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.FileDiffArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.FileDiff(ctx, p.FileDiffArgs)
	}))

	// M5c — conflict resolver
	s.Register("git.conflictFiles", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.ConflictFiles(ctx, git.ConflictFilesArgs{})
	}))

	s.Register("git.resolveConflict", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.ResolveConflictArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.ResolveConflict(ctx, p.ResolveConflictArgs)
	}))

	s.Register("git.fileContent", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.FileContentArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.FileContent(ctx, p.FileContentArgs)
	}))

	s.Register("git.writeAndStageFile", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.WriteAndStageArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.WriteAndStageFile(ctx, p.WriteAndStageArgs)
	}))

	s.Register("git.stagedFileDiff", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.StagedFileDiffArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.StagedFileDiff(ctx, p.StagedFileDiffArgs)
	}))

	s.Register("git.continueCherry", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.ContinueCherry(ctx, struct{}{})
	}))

	// M16 — AI conflict resolution helpers
	s.Register("git.restoreConflict", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.RestoreConflictArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.RestoreConflict(ctx, p.RestoreConflictArgs)
	}))

	s.Register("git.diffTexts", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.DiffTextsArgs
	}) (any, error) {
		// repo is unused (diff --no-index), but accepted for call-shape consistency.
		_ = p.Repo
		var r git.Repo
		return r.DiffTexts(ctx, p.DiffTextsArgs)
	}))

	// git.cherry — detect already-applied commits (patch equivalence check)
	s.Register("git.cherry", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.CherryArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.Cherry(ctx, p.CherryArgs)
	}))

	// M8 — external tool support
	s.Register("git.extractDiffFiles", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.ExtractDiffFilesArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.ExtractDiffFiles(ctx, p.ExtractDiffFilesArgs)
	}))

	s.Register("git.extractConflictFiles", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.ExtractConflictFilesArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.ExtractConflictFiles(ctx, p.ExtractConflictFilesArgs)
	}))

	s.Register("git.stageResolvedFile", wrap1(func(ctx context.Context, p struct {
		Repo string `json:"repo"`
		git.StageResolvedFileArgs
	}) (any, error) {
		r, err := git.Open(ctx, p.Repo)
		if err != nil {
			return nil, err
		}
		return r.StageResolvedFile(ctx, p.StageResolvedFileArgs)
	}))

	s.Register("git.cleanupTmpDir", wrap1(func(ctx context.Context, p struct {
		git.CleanupTmpDirArgs
	}) (any, error) {
		return git.CleanupTmpDir(ctx, p.CleanupTmpDirArgs)
	}))
}

// wrap1 reduces boilerplate for handlers that take a single typed params
// struct. It handles params decoding and translates *git.Error to *rpc.Error
// transparently; any other error becomes -32603 (Internal error).
func wrap1[P any](fn func(ctx context.Context, params P) (any, error)) rpc.Handler {
	return func(ctx context.Context, raw json.RawMessage) (any, *rpc.Error) {
		var p P
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, &rpc.Error{Code: -32602, Message: "Invalid params: " + err.Error()}
			}
		}
		result, err := fn(ctx, p)
		if err != nil {
			return nil, toRPCError(err)
		}
		return result, nil
	}
}

func toRPCError(err error) *rpc.Error {
	if e, ok := err.(*git.Error); ok {
		return &rpc.Error{Code: e.Code, Message: e.Message, Data: e.Data}
	}
	if e, ok := err.(*rpc.Error); ok {
		return e
	}
	return &rpc.Error{Code: -32603, Message: "Internal error: " + err.Error()}
}

func gitVersion() string {
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return "error: " + err.Error()
	}
	return strings.TrimSpace(string(out))
}

