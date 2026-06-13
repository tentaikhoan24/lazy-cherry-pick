package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lazy-cherry-pick/sidecar/internal/git"
)

// tool is one MCP tool: a name, a JSON Schema for its input, and a handler that
// decodes the arguments, calls into internal/git, and returns an MCP result.
type tool struct {
	name        string
	description string
	inputSchema map[string]any
	handler     func(ctx context.Context, s *Server, args json.RawMessage) (callToolResult, error)
}

func (t *tool) run(ctx context.Context, s *Server, args json.RawMessage) (callToolResult, error) {
	return t.handler(ctx, s, args)
}

func (s *Server) add(t *tool) {
	s.tools[t.name] = t
	s.order = append(s.order, t.name)
}

// schema helpers keep the JSON Schema definitions terse.
func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}
func str(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any  { return map[string]any{"type": "integer", "description": desc} }
func boolp(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
func arrStr(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// resolveRepo applies the repo-resolution policy: explicit arg → server default
// (LCP_DEFAULT_REPO) → error. Tools that touch a repo call this first.
func (s *Server) resolveRepo(repo string) (string, error) {
	if repo != "" {
		return repo, nil
	}
	if s.defaultRepo != "" {
		return s.defaultRepo, nil
	}
	return "", fmt.Errorf("no repo specified and LCP_DEFAULT_REPO is not set; pass the `repo` argument")
}

// ok builds a successful tool result with a human-readable text block plus the
// structured payload. The text is the JSON so an AI can read either form.
func ok(structured any) (callToolResult, error) {
	b, _ := json.MarshalIndent(structured, "", "  ")
	return callToolResult{
		Content:           []contentBlock{{Type: "text", Text: string(b)}},
		StructuredContent: structured,
	}, nil
}

// openRepo is a small helper to resolve + open a repo in one step.
func (s *Server) openRepo(ctx context.Context, repo string) (*git.Repo, error) {
	path, err := s.resolveRepo(repo)
	if err != nil {
		return nil, err
	}
	return git.Open(ctx, path)
}

// registerTools wires every MCP tool. Read tools first, then write tools, then
// the conflict-resolution loop tools.
func registerTools(s *Server) {
	// ── read tools ────────────────────────────────────────────────────────────

	s.add(&tool{
		name:        "list_commits",
		description: "List commits on a branch/ref with optional filters (author, message substring, since/until dates, path glob). Read-only.",
		inputSchema: obj(map[string]any{
			"repo":    str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"ref":     str("Branch or ref to list from. Default HEAD."),
			"limit":   num("Max commits to return. Default 100."),
			"author":  str("Filter by author (substring)."),
			"message": str("Filter commits whose subject/body contains this string."),
			"since":   str("Only commits after this date (e.g. '2026-01-01' or '2 weeks ago')."),
			"until":   str("Only commits before this date."),
			"path":    str("Only commits touching files matching this glob."),
		}),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo    string `json:"repo"`
				Ref     string `json:"ref"`
				Limit   int    `json:"limit"`
				Author  string `json:"author"`
				Message string `json:"message"`
				Since   string `json:"since"`
				Until   string `json:"until"`
				Path    string `json:"path"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			commits, err := r.Commits(ctx, git.CommitsArgs{
				Ref:   a.Ref,
				Limit: a.Limit,
				Filter: git.CommitFilter{
					Author:          a.Author,
					MessageContains: a.Message,
					Since:           a.Since,
					Until:           a.Until,
					PathGlob:        a.Path,
				},
			})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(map[string]any{"commits": commits, "count": len(commits)})
		},
	})

	s.add(&tool{
		name:        "list_branches",
		description: "List local (and optionally remote) branches. Read-only.",
		inputSchema: obj(map[string]any{
			"repo":          str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"includeRemote": boolp("Include remote-tracking branches. Default false."),
		}),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo          string `json:"repo"`
				IncludeRemote bool   `json:"includeRemote"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			branches, err := r.Branches(ctx, a.IncludeRemote)
			if err != nil {
				return callToolResult{}, err
			}
			return ok(map[string]any{"branches": branches})
		},
	})

	s.add(&tool{
		name:        "get_commit_detail",
		description: "Get full metadata (subject, body, author, date, parents) and changed files for a commit. Read-only.",
		inputSchema: obj(map[string]any{
			"repo": str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"sha":  str("Commit SHA (full or abbreviated)."),
		}, "sha"),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo string `json:"repo"`
				Sha  string `json:"sha"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			detail, err := r.CommitDetail(ctx, git.CommitDetailArgs{Sha: a.Sha})
			if err != nil {
				return callToolResult{}, err
			}
			files, err := r.CommitFiles(ctx, git.CommitFilesArgs{Sha: a.Sha})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(map[string]any{"detail": detail, "files": files})
		},
	})

	s.add(&tool{
		name:        "get_file_diff",
		description: "Get the unified diff of a single file as changed by a commit (git show). Read-only. Useful to review the actual change before cherry-picking, or while investigating a conflict.",
		inputSchema: obj(map[string]any{
			"repo": str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"sha":  str("Commit SHA (full or abbreviated)."),
			"file": str("Path of the file, relative to the repo root."),
		}, "sha", "file"),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo string `json:"repo"`
				Sha  string `json:"sha"`
				File string `json:"file"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			res, err := r.FileDiff(ctx, git.FileDiffArgs{Sha: a.Sha, File: a.File})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(res)
		},
	})

	s.add(&tool{
		name:        "find_already_applied",
		description: "Find which commits in `source` are already present (as equivalent patches) in `target`, using `git cherry`. Use this before cherry-picking to skip duplicates. Read-only.",
		inputSchema: obj(map[string]any{
			"repo":     str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"source":   str("The branch whose commits you want to check."),
			"target":   str("The branch to check against (upstream)."),
			"maxCount": num("Limit comparison to the last N commits. 0 = no limit."),
		}, "source", "target"),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo     string `json:"repo"`
				Source   string `json:"source"`
				Target   string `json:"target"`
				MaxCount int    `json:"maxCount"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			shas, err := r.Cherry(ctx, git.CherryArgs{Source: a.Source, Target: a.Target, MaxCount: a.MaxCount})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(map[string]any{"alreadyApplied": shas, "count": len(shas)})
		},
	})

	s.add(&tool{
		name:        "dry_run_pick",
		description: "Preview whether each commit would conflict if cherry-picked onto `target`, WITHOUT modifying the repo. Read-only.",
		inputSchema: obj(map[string]any{
			"repo":   str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"target": str("The branch to test applying onto. Default current HEAD."),
			"shas":   arrStr("Commit SHAs to test, in order."),
		}, "shas"),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo   string   `json:"repo"`
				Target string   `json:"target"`
				Shas   []string `json:"shas"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			res, err := r.DryRunPick(ctx, git.DryRunArgs{Target: a.Target, Shas: a.Shas})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(res)
		},
	})

	s.add(&tool{
		name:        "compare_branches",
		description: "List the commits in `head` that are not in `base` (what a PR from head into base would propose). Read-only.",
		inputSchema: obj(map[string]any{
			"repo":  str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"base":  str("The branch to merge into."),
			"head":  str("The branch carrying the changes."),
			"limit": num("Max commits to return. Default 100."),
		}, "base", "head"),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo  string `json:"repo"`
				Base  string `json:"base"`
				Head  string `json:"head"`
				Limit int    `json:"limit"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			res, err := r.CompareBranches(ctx, git.CompareArgs{Base: a.Base, Head: a.Head, Limit: a.Limit})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(res)
		},
	})

	s.add(&tool{
		name:        "default_branch",
		description: "Resolve the repo's default branch (via origin/HEAD, falling back to main/master/develop). Useful as the PR base. Read-only.",
		inputSchema: obj(map[string]any{
			"repo":   str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"remote": str("Remote name. Default origin."),
		}),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo   string `json:"repo"`
				Remote string `json:"remote"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			name, err := r.DefaultBranch(ctx, git.DefaultBranchArgs{Remote: a.Remote})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(map[string]any{"defaultBranch": name})
		},
	})

	s.add(&tool{
		name:        "get_status",
		description: "Get the working tree status: current branch, detached HEAD, upstream tracking branch, ahead/behind counts vs upstream, and staged/unstaged/untracked files. Read-only. Check this before operations that require a clean tree (e.g. cherry_pick onto a different target) or before fetch/pull/push.",
		inputSchema: obj(map[string]any{
			"repo": str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
		}),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo string `json:"repo"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			st, err := r.Status(ctx)
			if err != nil {
				return callToolResult{}, err
			}
			return ok(st)
		},
	})

	s.add(&tool{
		name:        "list_remotes",
		description: "List configured remotes (name, fetch URL, push URL). Read-only. Use to find a valid `remote` name for fetch/pull/push.",
		inputSchema: obj(map[string]any{
			"repo": str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
		}),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo string `json:"repo"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			remotes, err := r.Remotes(ctx, struct{}{})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(map[string]any{"remotes": remotes})
		},
	})

	// ── write tools ───────────────────────────────────────────────────────────

	s.add(&tool{
		name: "cherry_pick",
		description: "Apply commits onto `target` sequentially. WRITE operation — modifies the repo. " +
			"On a conflict it stops and returns status 'needs_human_resolution' with the conflicting files; " +
			"use get_conflict_files / get_conflict_content / resolve_conflict_file / continue_cherry_pick to finish, " +
			"or abort_cherry_pick to roll back. Already-applied commits are auto-skipped.",
		inputSchema: obj(map[string]any{
			"repo":     str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"target":   str("The branch to apply onto. If different from current HEAD, the working tree must be clean."),
			"shas":     arrStr("Commit SHAs to apply, in order."),
			"strategy": str("Merge strategy: 'smart' (default), 'theirs', or 'ours'."),
		}, "shas"),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo     string   `json:"repo"`
				Target   string   `json:"target"`
				Shas     []string `json:"shas"`
				Strategy string   `json:"strategy"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			res, err := r.CherryPick(ctx, git.CherryPickArgs{Target: a.Target, Shas: a.Shas, Strategy: a.Strategy})
			if err != nil {
				// A conflict surfaces as *git.Error with code CodeCherryPickConflict;
				// translate to a structured 'needs_human_resolution' result rather
				// than a tool error so the AI can drive the resolution loop.
				if ge, ok := err.(*git.Error); ok && ge.Code == git.CodeCherryPickConflict {
					applied, _ := ge.Data["applied"].([]string)
					conflicts, _ := ge.Data["conflicts"].([]git.ConflictInfo)
					payload := map[string]any{
						"status":    "needs_human_resolution",
						"applied":   applied,
						"conflicts": conflicts,
						"hint": "Conflict reached. Read each conflicting file with get_conflict_content, " +
							"write a merged version with resolve_conflict_file, then call continue_cherry_pick. " +
							"Or call abort_cherry_pick to roll back. Always let the user review before continuing.",
					}
					b, _ := json.MarshalIndent(payload, "", "  ")
					return callToolResult{
						Content:           []contentBlock{{Type: "text", Text: string(b)}},
						StructuredContent: payload,
					}, nil
				}
				return callToolResult{}, err
			}
			payload := map[string]any{
				"status":  "done",
				"applied": res.Applied,
				"skipped": res.Skipped,
			}
			return ok(payload)
		},
	})

	s.add(&tool{
		name:        "abort_cherry_pick",
		description: "Abort an in-progress cherry-pick and restore the repo to a clean state (git cherry-pick --abort). WRITE operation.",
		inputSchema: obj(map[string]any{
			"repo": str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
		}),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo string `json:"repo"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			if err := r.Abort(ctx); err != nil {
				return callToolResult{}, err
			}
			return ok(map[string]any{"aborted": true})
		},
	})

	s.add(&tool{
		name:        "fetch",
		description: "Fetch updates from a remote (git fetch --prune <remote>), updating remote-tracking refs (e.g. origin/main) without touching local branches or the working tree. WRITE operation (network access), but does not modify any local branch.",
		inputSchema: obj(map[string]any{
			"repo":   str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"remote": str("Remote name. Default origin."),
		}),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo   string `json:"repo"`
				Remote string `json:"remote"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			res, err := r.Fetch(ctx, git.FetchArgs{Remote: a.Remote})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(res)
		},
	})

	s.add(&tool{
		name: "pull",
		description: "Fast-forward a local branch from its remote counterpart (git fetch <remote> <branch>:<branch>). " +
			"Fails safely if the update is not a fast-forward — never creates a merge commit. WRITE operation that updates a local branch ref.",
		inputSchema: obj(map[string]any{
			"repo":   str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"branch": str("The local branch to fast-forward."),
			"remote": str("Remote name. Default origin."),
		}, "branch"),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo   string `json:"repo"`
				Branch string `json:"branch"`
				Remote string `json:"remote"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			res, err := r.Pull(ctx, git.PullArgs{Branch: a.Branch, Remote: a.Remote})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(res)
		},
	})

	s.add(&tool{
		name: "push",
		description: "Push a local branch to a remote (git push <remote> <branch>). WRITE operation that updates SHARED " +
			"remote state visible to other people — always confirm the exact remote and branch with the user before calling this.",
		inputSchema: obj(map[string]any{
			"repo":   str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"branch": str("The local branch to push."),
			"remote": str("Remote name. Default origin."),
		}, "branch"),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo   string `json:"repo"`
				Branch string `json:"branch"`
				Remote string `json:"remote"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			res, err := r.Push(ctx, git.PushArgs{Branch: a.Branch, Remote: a.Remote})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(res)
		},
	})

	s.add(&tool{
		name:        "create_branch",
		description: "Create a new local branch (git branch <name> [base]) without checking it out. WRITE operation. Returns the SHA the new branch points to.",
		inputSchema: obj(map[string]any{
			"repo": str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"name": str("Name of the new branch."),
			"base": str("Branch/ref to base it on. Default HEAD."),
		}, "name"),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo string `json:"repo"`
				Name string `json:"name"`
				Base string `json:"base"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			res, err := r.CreateBranch(ctx, git.CreateBranchArgs{Name: a.Name, Base: a.Base})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(res)
		},
	})

	// ── conflict-resolution loop tools ─────────────────────────────────────────

	s.add(&tool{
		name:        "get_conflict_files",
		description: "List files currently in merge-conflict state in the working tree. Read-only.",
		inputSchema: obj(map[string]any{
			"repo": str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
		}),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo string `json:"repo"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			res, err := r.ConflictFiles(ctx, git.ConflictFilesArgs{})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(res)
		},
	})

	s.add(&tool{
		name: "get_conflict_content",
		description: "Read a conflicted file's full content, including the `<<<<<<<` / `=======` / `>>>>>>>` " +
			"conflict markers, so you can produce a merged version. Read-only.",
		inputSchema: obj(map[string]any{
			"repo": str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"file": str("Path of the conflicted file, relative to the repo root."),
		}, "file"),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo string `json:"repo"`
				File string `json:"file"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			res, err := r.FileContent(ctx, git.FileContentArgs{File: a.File})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(map[string]any{"file": a.File, "content": res.Content})
		},
	})

	s.add(&tool{
		name:        "get_staged_diff",
		description: "Get the unified diff of a staged file vs HEAD (git diff --cached). Read-only. Use after resolve_conflict_file or resolve_conflict_side to verify the staged resolution looks correct before calling continue_cherry_pick.",
		inputSchema: obj(map[string]any{
			"repo": str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"file": str("Path of the file, relative to the repo root."),
		}, "file"),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo string `json:"repo"`
				File string `json:"file"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			res, err := r.StagedFileDiff(ctx, git.StagedFileDiffArgs{File: a.File})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(res)
		},
	})

	s.add(&tool{
		name: "resolve_conflict_file",
		description: "Write a merged version of a conflicted file (with ALL conflict markers removed) and stage it. " +
			"WRITE operation. IMPORTANT: always show the user your proposed merge and get approval before calling this — " +
			"a wrong resolution can silently lose code. The content must NOT contain any `<<<<<<<`, `=======`, or `>>>>>>>` markers.",
		inputSchema: obj(map[string]any{
			"repo":    str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"file":    str("Path of the conflicted file, relative to the repo root."),
			"content": str("The fully-merged file content with all conflict markers resolved/removed."),
		}, "file", "content"),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo    string `json:"repo"`
				File    string `json:"file"`
				Content string `json:"content"`
			}
			_ = json.Unmarshal(raw, &a)
			// Guard: refuse to stage content that still has conflict markers.
			for _, marker := range []string{"<<<<<<<", "=======", ">>>>>>>"} {
				if containsLineMarker(a.Content, marker) {
					return callToolResult{}, fmt.Errorf("content still contains conflict marker %q — provide a fully merged version", marker)
				}
			}
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			if _, err := r.WriteAndStageFile(ctx, git.WriteAndStageArgs{File: a.File, Content: a.Content}); err != nil {
				return callToolResult{}, err
			}
			return ok(map[string]any{"file": a.File, "staged": true})
		},
	})

	s.add(&tool{
		name: "resolve_conflict_side",
		description: "Resolve a conflicted file by taking one side entirely (git checkout --ours|--theirs, then git add). " +
			"WRITE operation. Faster than resolve_conflict_file when the correct resolution is simply 'keep one side as-is' — " +
			"confirm with the user which side first. For a manual/partial merge, use resolve_conflict_file instead.",
		inputSchema: obj(map[string]any{
			"repo":     str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
			"file":     str("Path of the conflicted file, relative to the repo root."),
			"strategy": str("Which side to keep: \"ours\" or \"theirs\"."),
		}, "file", "strategy"),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo     string `json:"repo"`
				File     string `json:"file"`
				Strategy string `json:"strategy"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			if _, err := r.ResolveConflict(ctx, git.ResolveConflictArgs{File: a.File, Strategy: a.Strategy}); err != nil {
				return callToolResult{}, err
			}
			return ok(map[string]any{"file": a.File, "strategy": a.Strategy, "staged": true})
		},
	})

	s.add(&tool{
		name: "continue_cherry_pick",
		description: "Resume a cherry-pick after all conflicts in the current commit are resolved and staged " +
			"(git cherry-pick --continue). WRITE operation. If the resolved commit became empty it is skipped automatically. " +
			"Note: this only advances ONE commit; if the original cherry_pick had more commits queued, re-run cherry_pick " +
			"with the remaining SHAs.",
		inputSchema: obj(map[string]any{
			"repo": str("Absolute path to the git repository. Omit to use LCP_DEFAULT_REPO."),
		}),
		handler: func(ctx context.Context, s *Server, raw json.RawMessage) (callToolResult, error) {
			var a struct {
				Repo string `json:"repo"`
			}
			_ = json.Unmarshal(raw, &a)
			r, err := s.openRepo(ctx, a.Repo)
			if err != nil {
				return callToolResult{}, err
			}
			res, err := r.ContinueCherry(ctx, struct{}{})
			if err != nil {
				return callToolResult{}, err
			}
			return ok(map[string]any{"done": res.Done})
		},
	})
}

// containsLineMarker reports whether content has a line that starts with the
// given conflict marker. Git conflict markers always sit at column 0, so we
// check the very start of the file and the start of every subsequent line.
func containsLineMarker(content, marker string) bool {
	return strings.HasPrefix(content, marker) || strings.Contains(content, "\n"+marker)
}
