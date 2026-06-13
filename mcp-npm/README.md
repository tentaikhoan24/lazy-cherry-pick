# lazy-cherry-pick-mcp

A [Model Context Protocol](https://modelcontextprotocol.io) server exposing **22 git
tools** (status, branches, commits, diffs, cherry-pick, fetch/pull/push, conflict
resolution) so AI clients — Claude Desktop, Cursor, VS Code, etc. — can drive real
git workflows on a repo on your machine, including an AI-driven conflict-resolution
loop.

This package is a thin launcher: on first run it downloads the matching
[`sidecar`](https://github.com/tentaikhoan24/lazy-cherry-pick) binary from the
project's GitHub Releases, caches it locally, and execs it with `--mcp`. No Tauri /
desktop app install required.

> **Platform support**: Windows x64 only for now. Other platforms exit with an error
> pointing to build-from-source instructions.

## Usage

Add to your MCP client config (e.g. Claude Desktop's `claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "lazy-cherry-pick": {
      "command": "npx",
      "args": ["-y", "lazy-cherry-pick-mcp"],
      "env": {
        "LCP_DEFAULT_REPO": "D:\\path\\to\\your\\repo"
      }
    }
  }
}
```

- `LCP_DEFAULT_REPO` is optional. Without it, the model must pass an absolute `repo`
  path on every tool call.
- The first call may take a few seconds while the sidecar binary downloads; it is
  cached afterwards (per-version) under `%LOCALAPPDATA%\lazy-cherry-pick-mcp\`.

## Integrity

Each release of this package pins the SHA256 checksum of the matching `sidecar`
binary (`sidecarSha256` in `package.json`, computed by CI from the exact binary
uploaded to the GitHub Release). On first run, `cli.js` hashes the downloaded
binary and refuses to cache or execute it if the checksum doesn't match.

## Tool catalog & conflict-resolution loop

See [docs/MCP.md](https://github.com/tentaikhoan24/lazy-cherry-pick/blob/master/docs/MCP.md)
in the main repo for the full 22-tool catalog, the AI-driven conflict-resolution
loop, and protocol details.

## Troubleshooting

- **404 downloading the sidecar binary right after a new version is published**: the
  corresponding GitHub Release may still be a draft. Wait for it to be published, or
  use the previous version pinned (`npx lazy-cherry-pick-mcp@<previous-version>`).
- **Unsupported platform error**: only Windows x64 has a prebuilt binary today. Build
  the sidecar from source (see the main repo README) and point your MCP client's
  `command` directly at the resulting `.exe` with `args: ["--mcp"]`.
