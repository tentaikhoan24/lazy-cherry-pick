// Forge URL builder — given a git remote URL, detect the hosting provider
// (GitHub / GitLab / Bitbucket) and build web URLs for commits, branches, files.
//
// Supports:
//   - https://github.com/owner/repo.git
//   - git@github.com:owner/repo.git
//   - ssh://git@github.com/owner/repo.git
//   - https://gitlab.example.com/group/subgroup/repo.git (self-hosted, subgroups)
//   - https://bitbucket.org/workspace/repo.git
//
// Self-hosted instances (GHE, GitLab self-managed) are inferred by URL pattern
// when possible, with a "generic" fallback for unrecognized hosts.

export type DetectedForgeKind = "github" | "gitlab" | "bitbucket" | "unknown";

export interface ForgeInfo {
  kind: DetectedForgeKind;
  host: string;        // e.g. "github.com" or "gitlab.example.com"
  path: string;        // e.g. "owner/repo" or "group/subgroup/repo" (no .git, no leading slash)
  webBaseUrl: string;  // e.g. "https://github.com/owner/repo"
}

/** Parse a git remote URL into a normalized {host, path} pair. */
function parseRemoteUrl(url: string): { host: string; path: string } | null {
  if (!url) return null;
  const trimmed = url.trim();

  // SSH shorthand: git@host:path
  const sshShorthand = /^([^@:]+@)?([^:]+):(.+?)(?:\.git)?$/;
  // HTTPS / SSH URL: scheme://[user@]host[:port]/path
  const fullUrl = /^[a-z]+:\/\/(?:[^@/]+@)?([^/:]+)(?::\d+)?\/(.+?)(?:\.git)?$/i;

  let m = trimmed.match(fullUrl);
  if (m) {
    return { host: m[1].toLowerCase(), path: m[2].replace(/\/+$/, "") };
  }
  m = trimmed.match(sshShorthand);
  if (m && !m[2].includes("/")) {
    return { host: m[2].toLowerCase(), path: m[3].replace(/\/+$/, "") };
  }
  return null;
}

/** Detect the forge kind from a host name. */
function detectKind(host: string): DetectedForgeKind {
  if (host === "github.com" || host.endsWith(".github.com")) return "github";
  if (host === "gitlab.com" || host.startsWith("gitlab.")) return "gitlab";
  if (host === "bitbucket.org" || host.startsWith("bitbucket.")) return "bitbucket";
  return "unknown";
}

export function parseForge(remoteUrl: string): ForgeInfo | null {
  const parsed = parseRemoteUrl(remoteUrl);
  if (!parsed) return null;
  const kind = detectKind(parsed.host);
  return {
    kind,
    host: parsed.host,
    path: parsed.path,
    webBaseUrl: `https://${parsed.host}/${parsed.path}`,
  };
}

/** Strip the remote name prefix from a branch (e.g. "origin/main" → "main"). */
export function stripRemotePrefix(branch: string, remoteName: string): string {
  const prefix = remoteName + "/";
  return branch.startsWith(prefix) ? branch.slice(prefix.length) : branch;
}

// ── URL builders ───────────────────────────────────────────────────────────
// Each provider has its own conventions; keep these in one place so the rest
// of the app just calls forgeCommitUrl(info, sha).

export function forgeCommitUrl(info: ForgeInfo, sha: string): string {
  switch (info.kind) {
    case "github":
      return `${info.webBaseUrl}/commit/${sha}`;
    case "gitlab":
      return `${info.webBaseUrl}/-/commit/${sha}`;
    case "bitbucket":
      return `${info.webBaseUrl}/commits/${sha}`;
    default:
      return `${info.webBaseUrl}/commit/${sha}`;
  }
}

export function forgeBranchUrl(info: ForgeInfo, branch: string): string {
  switch (info.kind) {
    case "github":
      return `${info.webBaseUrl}/tree/${encodeURIComponent(branch)}`;
    case "gitlab":
      return `${info.webBaseUrl}/-/tree/${encodeURIComponent(branch)}`;
    case "bitbucket":
      return `${info.webBaseUrl}/src/${encodeURIComponent(branch)}`;
    default:
      return `${info.webBaseUrl}/tree/${encodeURIComponent(branch)}`;
  }
}

export function forgeFileUrl(info: ForgeInfo, sha: string, path: string): string {
  switch (info.kind) {
    case "github":
      return `${info.webBaseUrl}/blob/${sha}/${path}`;
    case "gitlab":
      return `${info.webBaseUrl}/-/blob/${sha}/${path}`;
    case "bitbucket":
      return `${info.webBaseUrl}/src/${sha}/${path}`;
    default:
      return `${info.webBaseUrl}/blob/${sha}/${path}`;
  }
}

/** Human-friendly label for the provider — used in tooltips. */
export function forgeLabel(kind: DetectedForgeKind): string {
  switch (kind) {
    case "github": return "GitHub";
    case "gitlab": return "GitLab";
    case "bitbucket": return "Bitbucket";
    default: return "remote";
  }
}
