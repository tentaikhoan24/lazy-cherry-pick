# Lazy Cherry Pick

A desktop Git client focused on **batch cherry-pick workflows**: multi-select commits across branches, predict and preview conflicts, apply with smart filters, and open PRs — plus an **MCP server** so AI assistants can drive the same git operations.

> Status: **M15a complete** — full desktop UI (commit picking, dry-run conflicts, 3-pane conflict editor, side-by-side diff viewer, settings, external diff/merge tools, auto-updater, GitHub/GitLab/Bitbucket PR creation) **and** an MCP server (`--mcp` flag) exposing 22 git tools to AI clients — covering nearly all desktop git features (status, branches, diffs, cherry-pick, fetch/pull/push, conflict resolution) with an AI-driven conflict-resolution loop. See [Roadmap](#roadmap) and [MCP server / AI integration](#mcp-server--ai-integration).

## Why

`git cherry-pick` works one commit at a time and gives no preview. Tools like GitKraken/Sourcetree treat it as a 1-click operation, not a workflow. This project treats batch cherry-pick as the primary workflow: pick many commits, dry-run conflicts, resolve with a TortoiseGit-style 3-pane editor, apply atomically, push, and open a PR — all from one screen.

## Architecture

Hybrid: **Tauri 2** (Rust + Svelte 5) desktop shell drives a **Go sidecar** over newline-delimited JSON-RPC 2.0 on stdin/stdout. The sidecar shells directly to `git` CLI — no library, no fork.

```
+----------------------------+
|  Tauri desktop window      |
|  +----------+ +----------+ |
|  | Svelte 5 |<-> Rust    | |
|  | frontend |  | backend | |
|  +----------+ +-----+----+ |
+----------------------+-----+
                       | spawn + stdio (one process per call, multi-line for progress)
                       v
                +------------------+
                | Go sidecar       |   only this layer
                | JSON-RPC server  | — shells out to `git` —
                +------------------+
                       |
                       v
                    `git` CLI
```

The same sidecar binary also runs standalone as an **MCP server** (`sidecar.exe --mcp`) — AI clients talk JSON-RPC 2.0 over the same stdio transport, with a different method set (`tools/list`, `tools/call`, ...). The `--mcp` check happens first thing in `main()`, so this has **zero impact** on the Tauri desktop app. See [MCP server / AI integration](#mcp-server--ai-integration).

## Layout

```
.
├── app/
│   ├── src/
│   │   ├── lib/
│   │   │   ├── rpc-types.ts, rpc.ts        # Typed IPC layer (mirrors Go types)
│   │   │   ├── Toolbar.svelte, Settings.svelte, GitConsole.svelte
│   │   │   ├── Toast.svelte, UpdateBanner.svelte, ResultBanner.svelte
│   │   │   ├── BranchSelect.svelte, CommitList.svelte, PickQueue.svelte
│   │   │   ├── CommitDetail.svelte, CommitFiles.svelte, FileDiff.svelte
│   │   │   ├── ConflictResolver.svelte     # 3-pane merge editor support
│   │   │   └── ConnectForge.svelte, CreatePR.svelte, forge.ts  # GitHub/GitLab/Bitbucket PRs
│   │   └── routes/
│   │       ├── +page.svelte          # Main orchestrator (all app state)
│   │       ├── diff/+page.svelte     # File diff viewer window
│   │       └── conflict/+page.svelte # 3-pane conflict merge editor
│   └── src-tauri/
│       ├── src/lib.rs                # sidecar_call (HashMap concurrency), settings, recents, external tools
│       ├── src/forge.rs              # GitHub/GitLab/Bitbucket REST APIs + OS keychain (keyring)
│       ├── Cargo.toml                # tauri-plugin-shell/dialog/updater/process, reqwest, keyring
│       ├── capabilities/default.json # shell:allow-* + dialog:allow-open; diff-*/conflict-* windows
│       └── binaries/                 # sidecar-<triple>.exe
├── sidecar/
│   ├── main.go                       # NDJSON dispatcher; --mcp flag branches to internal/mcp
│   ├── go.mod                        # module github.com/lazy-cherry-pick/sidecar, Go 1.23
│   └── internal/
│       ├── rpc/server.go             # NDJSON JSON-RPC 2.0 transport (Tauri ↔ sidecar)
│       ├── mcp/                      # MCP server (--mcp): 22 git tools for AI clients
│       └── git/                      # git ops: 24 files covering all RPC methods + MCP tools
├── docs/
│   ├── IPC.md                        # NDJSON protocol spec (Tauri ↔ sidecar)
│   └── MCP.md                        # MCP server protocol + tool catalog (AI clients)
├── dev.ps1                           # One-liner dev launcher (Windows)
└── CLAUDE.md                         # AI dev context (read first)
```

## Quick start

### Prerequisites (Windows)

- Node.js 18+, Rust stable (MSVC), Go 1.23+, Git, MSVC Build Tools 2022, WebView2

```powershell
winget install Rustlang.Rustup GoLang.Go OpenJS.NodeJS Git.Git
winget install Microsoft.VisualStudio.2022.BuildTools --override `
  "--quiet --wait --norestart --nocache --add Microsoft.VisualStudio.Workload.VCTools --includeRecommended"
rustup default stable
```

### Build & run

```powershell
$env:Path = "$env:USERPROFILE\.cargo\bin;C:\Program Files\Go\bin;" + $env:Path

# 1) Build Go sidecar (Tauri requires target-triple suffix in filename)
$triple = (rustc -vV | Select-String "host:").ToString().Split(":")[1].Trim()
Set-Location sidecar
go build -o ..\app\src-tauri\binaries\sidecar-$triple.exe .
Set-Location ..

# 2) Install frontend deps (first run only)
Set-Location app && npm install

# 3) Dev server
npm run tauri dev
```

Or run `.\dev.ps1` which handles PATH and launches the dev server.

### Run Go tests

```powershell
$env:Path = "C:\Program Files\Go\bin;" + $env:Path
Set-Location sidecar
go test ./internal/git/ -v
go test ./internal/mcp/ -v
```

Integration tests use real temp git repos (no mocks).

## Usage

1. Click **Open repo** → pick any Git repository folder (or use **Recent ▾** dropdown in toolbar)
2. **Source branch** dropdown (left) — select the branch to pick from
3. **Fetch ▾** button — fetch latest commits from remote; dropdown lets you switch to **Pull** (fast-forward only)
4. **Tick commits** in the list — they appear in the Pick queue (right), in selection order
   - ⚠ icon on queued commits means a conflict is predicted (dry-run preview, debounced)
   - Already-applied commits (`git cherry`) are dimmed and marked **Applied**
   - Click a commit to open the **detail panel** below (message body + file list with +/- stats)
   - Click a file in the detail panel to open the **side-by-side diff viewer** in a new window
5. **Target branch** dropdown (right) — where commits will land (default: current branch); **Create Branch** button to create a new branch on the fly
6. Click **Apply** (or use the ▾ dropdown for **Apply & Push → \<remote\>**, or **Apply & Push & Create PR**) — runs `git cherry-pick` sequentially:
   - Progress bar shows `n/total — sha` for each commit as it applies
   - **Cancel** button aborts mid-batch and restores the repo to a clean state
   - Already-applied (empty) commits are auto-skipped with a toast
   - On conflict: **ConflictResolver** panel appears
     - **Keep Ours / Use Theirs** — one-click resolution per file
     - Click filename → opens the **3-pane merge editor** (TortoiseGit style) with inline per-block action buttons, cross-pane combine, and keyboard navigation
     - **🤖 AI resolve all** (if enabled in Settings) — shells out to your installed headless AI CLI (Claude Code, etc.) to merge every conflicting file, then lets you **Review** each result (3-way view identical to the manual editor, or your external merge tool), **Accept & stage** or **Discard** — see [AI conflict resolution](#ai-conflict-resolution-desktop--ai-cli) below
     - **Continue →** after all files resolved; **Abort** to cancel
   - Result shown via Toast (success / skipped) or the conflict banner
7. **Settings** (gear icon) — theme, max commits, EOL markers, **AI conflict resolution** (headless AI CLI), external diff/merge tools (TortoiseGit/Beyond Compare/WinMerge/VS Code), auto-updater, and **Connect** GitHub/GitLab/Bitbucket for one-click PR/MR creation after applying

## AI conflict resolution (desktop → AI CLI)

The desktop app can shell out to your **installed headless AI CLI** to suggest a merge for cherry-pick conflicts. This is the *reverse* direction from the MCP server below: here the **app drives the AI**, not the other way around.

- **One button, all files**: in the ConflictResolver, **🤖 AI resolve all** runs the configured CLI once over every conflicting file so the model sees the whole picture.
- **Stability**: the app never parses the model's stdout for merged content — the AI agent (with Edit/Write) writes the merged file straight to disk and the app re-reads it. stdout JSON is used only for success/cost/error.
- **Review before stage (human-in-the-loop)**: each AI-resolved file shows **Review** → a 3-way view *identical to the manual merge editor* (Theirs / Ours / merged result), or your external merge tool if configured → **Accept & stage** or **Discard** (restores the conflict markers via `git checkout -m`).
- **Safety**: the CLI runs with shell/git/network tools disabled, so it can't commit/push and the index stays unmerged (Discard always works); files are verified marker-free before staging.
- **Provider-agnostic**: Settings → **AI Conflict Resolution** ships a verified **Claude Code** preset (uses your existing Claude login — no separate API key) plus editable presets for other CLIs (Gemini/Codex/Aider/Custom). Every AI invocation is also logged in the **Git Console** (🤖 entries, with cost + duration).

See [CLAUDE.md](./CLAUDE.md) (M16 series) for the design rationale and the exact CLI flags.

## MCP server / AI integration

The same sidecar binary doubles as a **Model Context Protocol (MCP) server**. Run it with `--mcp` and AI clients (Claude Desktop, Cursor, VS Code, etc.) can read commit history and drive cherry-pick / conflict-resolution / fetch / pull / push on a real repo through tool calls — **no Tauri/desktop install required**.

- **22 tools** (13 read-only, 9 write): covering nearly all desktop git features — explore branches/commits/diffs, check status/remotes, preview conflicts (`dry_run_pick`, `find_already_applied`, `compare_branches`), `cherry_pick`, then `fetch`/`pull`/`push`/`create_branch`. Forge (PR/MR) tools are deferred to M15b.
- **AI-driven conflict-resolution loop**: `cherry_pick` never hard-errors on conflict — it returns `needs_human_resolution` with the conflicting files and a hint, so the model can `get_conflict_content` → propose a merge → `resolve_conflict_file`/`resolve_conflict_side` → `continue_cherry_pick`.
- Repo resolution via an explicit `repo` argument per call, or an `LCP_DEFAULT_REPO` env var for "this one repo".
- The `initialize` response includes an `instructions` field summarizing this workflow for clients that surface it to the model.

**Quickest start (Windows x64) — via npx**, no download/path needed:

```json
{
  "mcpServers": {
    "lazy-cherry-pick": {
      "command": "npx",
      "args": ["-y", "lazy-cherry-pick-mcp"],
      "env": { "LCP_DEFAULT_REPO": "D:\\path\\to\\your\\repo" }
    }
  }
}
```

Or point at a downloaded/built sidecar binary directly:

```json
{
  "mcpServers": {
    "lazy-cherry-pick": {
      "command": "C:\\path\\to\\sidecar-x86_64-pc-windows-msvc.exe",
      "args": ["--mcp"],
      "env": { "LCP_DEFAULT_REPO": "D:\\path\\to\\your\\repo" }
    }
  }
}
```

See [docs/MCP.md](./docs/MCP.md) for the full protocol, tool catalog, and conflict-loop walkthrough — including how to get a standalone sidecar binary without installing the desktop app.

## Smoke-testing sidecar without Tauri

```powershell
$s = ".\app\src-tauri\binaries\sidecar-x86_64-pc-windows-msvc.exe"
'{"jsonrpc":"2.0","id":1,"method":"ping"}' | & $s
'{"jsonrpc":"2.0","id":2,"method":"git.branches","params":{"repo":"C:\\path\\to\\repo"}}' | & $s

# MCP mode:
'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}' | & $s --mcp
```

See [docs/IPC.md](./docs/IPC.md) for all NDJSON method signatures, or [docs/MCP.md](./docs/MCP.md) for MCP tools.

## Design decisions

- **Target branch Model B** — dropdown defaults to current branch, user can pick any; dirty-tree guard fires before checkout.
- **Sidecar is the only `git` caller** — Rust and Svelte never shell to git directly.
- **Direct git CLI, no library** — considered lazygit fork, too much coupling. Clean, testable Go split across 24 files in `internal/git`.
- **One process per RPC call (NDJSON mode)** — 30–50 ms overhead acceptable for user-initiated ops. Concurrent calls from multiple windows (main + conflict editor) are safe: `ActiveSidecar` is `Mutex<HashMap<u64, CommandChild>>` with per-call atomic IDs so calls can't kill each other.
- **Progress streaming** — `git.cherryPick` emits intermediate `progress` lines before the final `result`. Rust reads in a loop and forwards them as Tauri events (`cp-progress`) to the frontend.
- **Leave-in-conflict state** — on first conflict, cherry-pick stops but does not abort. The repo stays in conflict state so the frontend can drive a 3-way resolver. `git.continueCherry` completes the pick after all files are resolved.
- **Forge (PR/MR) integration lives in Rust, not the sidecar** — `forge.rs` talks directly to GitHub/GitLab/Bitbucket REST APIs via `reqwest`, with tokens in the OS keychain (`keyring`). The Go sidecar only ever shells to `git`.
- **MCP is a second mode of the same binary** — `--mcp` hand-rolls JSON-RPC 2.0 over stdio (stdlib only), reusing the same `internal/git` operations layer as the NDJSON desktop protocol. The MCP server is a single long-lived process, unlike the one-process-per-call NDJSON mode.

## Roadmap

- ✅ **M1** Scaffold: Tauri + Svelte + Go, JSON-RPC stdio, IPC validated end-to-end
- ✅ **M2** Git ops layer: `git.status/branches/commits/cherryPick`, integration tests, TypeScript wrapper
- ✅ **M3 / M3.1** Main UI (Toolbar, CommitList, PickQueue, ResultBanner) + Apply & Push dropdown
- ✅ **M4** Progress & UX: per-commit progress bar, Cancel/abort, recent repos, Fetch/Pull refresh
- ✅ **M5** Commit detail panels, dry-run conflict preview, side-by-side diff viewer, TortoiseGit-style 3-pane conflict editor
- ✅ **M6** Searchable branch dropdowns, commit filter bar + saved presets, conventional-commit/JIRA badges
- ✅ **M7** Settings panel (theme, max commits, EOL markers, auto-fetch) + realtime Git Console
- ✅ **M8** External diff/merge tools — TortoiseGit, Beyond Compare, WinMerge, VS Code, with auto-detect
- ✅ **M9** Auto-updater — in-app update check/download/install, CI-signed releases
- ✅ **M10** Keyboard nav, drag-drop reorder, undo (Ctrl+Z), Toast notifications, already-applied detection & auto-skip
- ✅ **M13** Multi-remote `Apply & Push → <remote>`, Copy SHA / Open commit in browser, forge URL parser
- ✅ **M14** PR/MR creation for GitHub, GitLab, and Bitbucket Cloud — OS-keychain tokens, PR status bar with hover preview, "Apply & Push & Create PR" flow
- ✅ **M15a** MCP server — sidecar doubles as a 22-tool MCP server (`--mcp`) for AI clients, with an AI-driven conflict-resolution loop
- ✅ **M16** AI conflict resolution (desktop → headless AI CLI) — "🤖 AI resolve all", review-before-stage with a 3-way view matching the manual editor (or external merge tool), provider-agnostic presets (Claude Code verified), AI calls logged in the Git Console
- 🔜 **M15b+** — forge tools over MCP, MCP Sampling, further polish

See [CLAUDE.md](./CLAUDE.md) for the full per-milestone changelog.

## License

TBD. Planning **MIT**.

## See also

- [CLAUDE.md](./CLAUDE.md) — AI dev context (read first when entering the repo)
- [docs/IPC.md](./docs/IPC.md) — NDJSON JSON-RPC protocol spec (Tauri ↔ sidecar)
- [docs/MCP.md](./docs/MCP.md) — MCP server protocol, tool catalog, AI client config
- [sidecar/README.md](./sidecar/README.md) — sidecar build, test, smoke test
