# MCP Server (M15a)

The sidecar binary doubles as a **Model Context Protocol (MCP) server**, letting AI
clients (Claude Desktop, Cursor, VS Code, etc.) read commit history and drive
cherry-pick / conflict-resolution / fetch / pull / push workflows on a real repo
through tool calls — covering essentially all desktop-app git features except
forge (PR/MR) creation, which is deferred to M15b.

> **Scope**: this document covers the `--mcp` mode only. For the NDJSON protocol
> used by the Tauri desktop app, see [IPC.md](IPC.md). The two modes are
> separate code paths in the same binary — see [Relationship to the desktop app](#relationship-to-the-desktop-app).

## Getting the binary

- **No desktop app install needed.** The sidecar binary is fully standalone —
  `--mcp` mode has zero dependency on Tauri/Rust/the frontend.
- **Easiest: npx** (Windows x64 only today) — see [Running via npx](#running-via-npx)
  below. No download step in your client config; `npx` fetches and caches the
  binary on first run.
- **Prebuilt**: each [GitHub Release](https://github.com/tentaikhoan24/lazy-cherry-pick/releases)
  attaches `sidecar-x86_64-pc-windows-msvc.exe` as a standalone download asset,
  separate from the `.msi` installer.
- **Build it yourself**:
  ```powershell
  $triple = (rustc -vV | Select-String "host:").ToString().Split(":")[1].Trim()
  cd sidecar
  go build -o ..\app\src-tauri\binaries\sidecar-$triple.exe .
  ```

## Running via npx

The [`lazy-cherry-pick-mcp`](https://www.npmjs.com/package/lazy-cherry-pick-mcp) npm
package is a thin launcher: on first run it downloads the matching
`sidecar-x86_64-pc-windows-msvc.exe` from this repo's GitHub Releases, verifies its
SHA256 against a checksum pinned in `package.json` (refusing to cache/run it on a
mismatch), caches it under `%LOCALAPPDATA%\lazy-cherry-pick-mcp\`, and execs it with
`--mcp`.

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

Windows x64 only for now — other platforms get a clear error pointing back to the
build-from-source instructions above. Source for the wrapper lives in
[`mcp-npm/`](../mcp-npm).

## Starting the server

```
sidecar-x86_64-pc-windows-msvc.exe --mcp
```

- Reads JSON-RPC 2.0 requests from **stdin**, one JSON object per line (NDJSON).
- Writes responses to **stdout**, one JSON object per line.
- Logs (including tool errors) go to **stderr** — never parsed by MCP clients.
- UTF-8 BOM on input lines is stripped, same as the NDJSON server.

### `LCP_DEFAULT_REPO`

Optional environment variable. If set, tool calls may omit the `repo` argument
and operate on this path. If unset and a tool call omits `repo`, the call fails
with an error asking for an explicit path. This lets a client be configured for
"this one repo" without the model needing to know the filesystem path.

## Protocol

MCP is JSON-RPC 2.0 over the same line-delimited stdio transport as the NDJSON
mode, but with a different method set and response envelope.

| Method | Direction | Notes |
|---|---|---|
| `initialize` | client → server | Returns `protocolVersion`, `capabilities: {"tools": {}}`, `serverInfo`, `instructions` |
| `notifications/initialized` | client → server | Notification (no `id`), no response |
| `notifications/cancelled` | client → server | Notification, no response |
| `ping` | client → server | Returns `{}` |
| `tools/list` | client → server | Returns all 22 tool definitions (name, description, JSON Schema) |
| `tools/call` | client → server | Invokes one tool by name with arguments |

Protocol version implemented: `2024-11-05`. Server identifies itself as
`lazy-cherry-pick` / current app version.

### `initialize` instructions

The `initialize` response includes an `instructions` string — a model-facing
hint (per the MCP spec) summarizing the suggested workflow (explore →
`dry_run_pick`/`find_already_applied` → `cherry_pick` → conflict loop) and the
rule that all WRITE tools need user confirmation. Clients that surface
`instructions` to the model give it this context automatically, without
needing this document pasted into the conversation. See
`serverInstructions` in `sidecar/internal/mcp/server.go` for the exact text.

### `tools/call` response shape

```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "result": {
    "content": [{ "type": "text", "text": "{ ...JSON... }" }],
    "structuredContent": { "...": "machine-readable payload" },
    "isError": false
  }
}
```

- `content[]` — human-readable text blocks (the same JSON as `structuredContent`,
  pretty-printed, so a model can read either).
- `structuredContent` — the machine-readable payload (omitted on tool errors).
- `isError: true` — the tool itself failed (unknown tool name, git error, etc.).
  This is **not** a JSON-RPC protocol error — the call still returns a normal
  `result`, per the MCP spec, so the model can see the error text and recover.

Protocol-level errors (bad JSON, unknown method, bad params) use the standard
`error` field with JSON-RPC codes (`-32700`, `-32601`, `-32602`).

## Tool catalog

All tools take an optional `repo` string argument (absolute path; falls back to
`LCP_DEFAULT_REPO`). Omitted below for brevity except where it's the only field.

### Read-only tools

| Tool | Description | Key arguments |
|---|---|---|
| `list_commits` | List commits on a branch/ref with optional filters | `ref`, `limit`, `author`, `message`, `since`, `until`, `path` |
| `list_branches` | List local (and optionally remote) branches | `includeRemote` |
| `get_commit_detail` | Full metadata + changed files for a commit | `sha` (required) |
| `get_file_diff` | Unified diff of a single file as changed by a commit (`git show`) | `sha`, `file` (required) |
| `find_already_applied` | Commits in `source` already present in `target` (`git cherry`) | `source`, `target` (required), `maxCount` |
| `dry_run_pick` | Preview conflicts for a list of SHAs without modifying the repo | `shas` (required), `target` |
| `compare_branches` | Commits in `head` not in `base` (what a PR would propose) | `base`, `head` (required), `limit` |
| `default_branch` | Resolve the repo's default branch (origin/HEAD → main/master/develop) | `remote` |
| `get_status` | Working tree status: branch, detached HEAD, upstream, ahead/behind, staged/unstaged/untracked files | — |
| `list_remotes` | List configured remotes (name, fetch URL, push URL) | — |
| `get_conflict_files` | List files currently in merge-conflict state | — |
| `get_conflict_content` | Read a conflicted file including `<<<<<<<`/`=======`/`>>>>>>>` markers | `file` (required) |
| `get_staged_diff` | Unified diff of a staged file vs HEAD (`git diff --cached`) | `file` (required) |

### Write tools

| Tool | Description | Key arguments |
|---|---|---|
| `cherry_pick` | Apply commits onto `target` sequentially. Already-applied commits auto-skip. Stops on conflict — see below | `shas` (required), `target`, `strategy` |
| `abort_cherry_pick` | `git cherry-pick --abort`, restoring a clean working tree | — |
| `fetch` | `git fetch --prune <remote>` — updates remote-tracking refs only, no local branch changes | `remote` |
| `pull` | Fast-forward a local branch from its remote (`git fetch <remote> <branch>:<branch>`); fails safely if not a fast-forward | `branch` (required), `remote` |
| `push` | `git push <remote> <branch>` — publishes a local branch to a remote. Always confirm remote+branch with the user first | `branch` (required), `remote` |
| `create_branch` | Create a new local branch without checking it out (`git branch <name> [base]`) | `name` (required), `base` |
| `resolve_conflict_file` | Write a fully-merged file (no conflict markers) and stage it | `file`, `content` (required) |
| `resolve_conflict_side` | Resolve a conflicted file by taking one side entirely (`git checkout --ours`/`--theirs` + `git add`) | `file`, `strategy` (required: `"ours"`/`"theirs"`) |
| `continue_cherry_pick` | `git cherry-pick --continue` after all conflicts in the current commit are resolved/staged | — |

`strategy` for `cherry_pick`: `"smart"` (default), `"theirs"`, or `"ours"`.

## Conflict-resolution loop

This is the core design point of M15a: **`cherry_pick` never leaves the AI stuck
on a hard error when a conflict occurs.** Instead it returns a structured
"pause" result so the AI can read the conflict, propose a fix, and resume —
with the user reviewing before anything is committed.

1. **`cherry_pick`** is called with one or more `shas`.
   - If a commit conflicts, the tool returns (as `structuredContent`, `isError: false`):
     ```json
     {
       "status": "needs_human_resolution",
       "applied": ["<shas applied before the conflict>"],
       "conflicts": [{ "...": "ConflictInfo per file" }],
       "hint": "Conflict reached. Read each conflicting file with get_conflict_content, write a merged version with resolve_conflict_file, then call continue_cherry_pick. Or call abort_cherry_pick to roll back. Always let the user review before continuing."
     }
     ```
   - If everything applies cleanly: `{"status": "done", "applied": [...], "skipped": [...]}`.
2. **`get_conflict_files`** — confirm which files are conflicted (matches `conflicts` above).
3. **`get_conflict_content`** — for each conflicted file, read the raw content
   including `<<<<<<<`/`=======`/`>>>>>>>` markers.
4. The AI proposes a merged version of the file. **The hosting client should
   show this to the user for review before proceeding** — the tool descriptions
   say so explicitly, but MCP itself does not enforce human-in-the-loop; that's
   a client/UX responsibility.
5. **`resolve_conflict_file`** — writes the merged content and `git add`s it.
   Guarded: if the content still contains a `<<<<<<<`, `=======`, or `>>>>>>>`
   line, the call fails with an error instead of staging a broken file.
   For the simpler case of "keep one side entirely", **`resolve_conflict_side`**
   (`strategy: "ours"` or `"theirs"`) does `git checkout --ours/--theirs` + `git add`
   in one step — confirm with the user which side first.
6. **`get_staged_diff`** — optionally review the staged resolution (`git diff
   --cached`) before continuing, to double-check it looks right.
7. Repeat steps 3–6 for any remaining conflicted files in the same commit.
8. **`continue_cherry_pick`** — `git cherry-pick --continue`. If the resolved
   commit became empty (the change was already effectively applied), it is
   skipped automatically (`{"done": true}`).
9. If the original `cherry_pick` call had more SHAs queued after the one that
   conflicted, **re-run `cherry_pick`** with the remaining SHAs — step 8 only
   advances one commit.
10. At any point, **`abort_cherry_pick`** rolls back to a clean working tree.

## Repo path resolution

For every tool, the repo is resolved as:

1. `repo` argument, if non-empty.
2. `LCP_DEFAULT_REPO` environment variable, if set.
3. Otherwise the call fails: `"no repo specified and LCP_DEFAULT_REPO is not set; pass the repo argument"`.

## Client configuration

> **Prefer npx?** See [Running via npx](#running-via-npx) above — no binary path to
> configure. The config below is for a binary you've downloaded or built yourself.

### Claude Desktop / Cursor / VS Code (generic MCP config)

Point `command` at the built sidecar binary and pass `--mcp`. On this machine the
binary is at `app/src-tauri/binaries/sidecar-x86_64-pc-windows-msvc.exe` (the
Tauri-bundled build, with the target-triple suffix required by Tauri).

```json
{
  "mcpServers": {
    "lazy-cherry-pick": {
      "command": "D:\\project\\lazy-cherry-pick\\app\\src-tauri\\binaries\\sidecar-x86_64-pc-windows-msvc.exe",
      "args": ["--mcp"],
      "env": {
        "LCP_DEFAULT_REPO": "D:\\path\\to\\your\\repo"
      }
    }
  }
}
```

- `LCP_DEFAULT_REPO` is optional. Without it, the model must pass an absolute
  `repo` path on every tool call (which it can discover by asking the user, or
  if the client exposes the workspace path some other way).
- To work across multiple repos without restarting the client, omit
  `LCP_DEFAULT_REPO` and pass `repo` explicitly per call.

## Relationship to the desktop app

- The `--mcp` flag is checked first thing in `main()` (`sidecar/main.go`). If
  present, the process runs `mcp.Serve(...)` and **never** touches the NDJSON
  RPC path (`internal/rpc`) that the Tauri app uses.
- The Tauri app never passes `--mcp` — `sidecar_call` always spawns the binary
  with no arguments. So enabling MCP support has **zero effect** on the desktop
  app's behavior.
- Both modes share `internal/git` (the actual git operations layer) — MCP tools
  are thin wrappers that map JSON arguments to the same `*git.Repo` methods the
  NDJSON handlers call.
- Unlike the NDJSON mode (one process per call, see
  [IPC.md](IPC.md#transport)), the MCP server is a **single long-lived process**
  — the MCP client starts it once and keeps the stdio pipes open for the whole
  session.

## Limitations / future work

- **Forge tools not exposed** (PR/MR creation, `list_prs`). That logic lives in
  the Rust layer (`app/src-tauri/src/forge.rs`) with OS-keychain token storage;
  porting it to the Go sidecar for MCP is deferred to **M15b** (would need a Go
  HTTP client + `go-keyring` reading the same keychain entries, service
  `lazy-cherry-pick.forge`).
- **No MCP Sampling** (server asking the client's model to generate text) —
  deferred to **M15d**. Today all "AI reasoning" about conflicts happens in the
  MCP *client* (e.g. Claude Desktop's model), not the server.
- No "list/select repo" tool — the model must already know (or be told) the
  absolute repo path, or `LCP_DEFAULT_REPO` must be set.
