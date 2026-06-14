// Wire types shared between Go sidecar and Svelte frontend.
// Keep in sync with sidecar/internal/git/types.go.

export interface FileStatus {
  path: string;
  status: string; // "M" | "A" | "D" | "R" | "C" | "U"
}

export interface RepoStatus {
  branch: string;
  upstream: string;
  ahead: number;
  behind: number;
  dirty: boolean;
  staged: FileStatus[];
  unstaged: FileStatus[];
  untracked: string[];
  detached: boolean;
}

export interface Branch {
  name: string;
  sha: string;
  isHead: boolean;
  upstream: string;
  remote: boolean;
}

export interface Remote {
  name: string;
  fetchUrl: string;
  pushUrl: string;
}

// ── M14 — Forge integration ────────────────────────────────
export type ForgeKind = "github" | "gitlab" | "bitbucket";

export interface ForgeConnection {
  kind: ForgeKind;
  baseURL: string;
  host: string;
  username: string;
}

export interface ForgeConnectionTest {
  username: string;
  scopes: string[];
}

export interface CreatePRArgs {
  /** owner/repo (GitHub) or namespace/path (GitLab including subgroups). */
  projectPath: string;
  base: string;
  head: string;
  title: string;
  body: string;
  draft: boolean;
}

export interface CreatePRResult {
  url: string;
  number: number;
  state: string;
  /** True when the PR/MR already existed before this call (server returned it
   * instead of creating a new one). Frontend uses this to swap the toast wording. */
  alreadyExisted: boolean;
}

export interface PRSummary {
  url: string;
  number: number;
  title: string;
  state: string;
  draft: boolean;
  /** Base branch the PR will merge into. */
  base: string;
  /** PR author username. */
  author: string;
  /** ISO 8601 — last update time. */
  updatedAt: string;
  /** ISO 8601 — when the PR was opened. */
  createdAt: string;
  /** First ~200 chars of the PR description (plain). */
  bodyPreview: string;
}

export interface CommitFilter {
  author?: string;
  messageContains?: string;
  since?: string;
  until?: string;
  pathGlob?: string;
}

export interface Commit {
  sha: string;
  parents: string[];
  author: string;
  email: string;
  time: number; // Unix timestamp (seconds)
  subject: string;
  refs: string[];
}

export interface ConflictInfo {
  sha: string;
  files: string[];
}

export interface CherryPickResult {
  applied: string[];
  skipped: string[];
  conflicts: ConflictInfo[];
}

export interface CherryPickProgress {
  n: number;
  total: number;
  sha: string;
}

export interface RecentRepo {
  path: string;
  lastOpened: number; // unix timestamp (seconds)
}

export interface PushResult {
  remote: string;
  branch: string;
}

export interface CreateBranchResult {
  name: string;
  sha: string;
}

export interface FetchResult {
  remote: string;
}

export interface PullResult {
  remote: string;
  branch: string;
}

export interface CommitDetail {
  sha: string;
  parents: string[];
  author: string;
  email: string;
  time: number;
  subject: string;
  body: string;
}

export interface CommitFile {
  path: string;
  added: number;
  removed: number;
  status: string; // M, A, D, R, C, T, U
}

export interface DryRunItem {
  sha: string;
  willConflict: boolean;
  files: string[];
}

export interface DryRunResult {
  results: DryRunItem[];
}

export interface FileDiffResult {
  sha: string;
  file: string;
  diff: string; // raw unified diff text
}

export interface OpenRepoResult {
  path: string;
  branch: string;
  detached: boolean;
  cherryPickHead?: string;
}

export interface ConflictFileInfo {
  path: string;
  status: string; // UU, AA, DD, AU, UA, DU, UD
}

export interface ConflictFilesResult {
  files: ConflictFileInfo[];
}

export interface ContinueCherryResult {
  done: boolean;
}

export interface FileContentResult {
  content: string;
}

export interface WriteAndStageResult {
  staged: boolean;
}

export interface AppSettings {
  maxCommits: number;
  defaultApplyMode: "apply" | "apply-push";
  showEolMarkers: boolean;
  autoFetchOnOpen: boolean;
  theme: "dark" | "light";
  externalDiffEnabled: boolean;
  externalDiffPath: string;
  externalDiffArgs: string;
  externalMergeEnabled: boolean;
  externalMergePath: string;
  externalMergeArgs: string;
  checkForUpdatesOnStartup: boolean;
  /** M11a — auto-stash uncommitted changes before a cherry-pick batch, pop after. */
  autoStash: boolean;
  /** M16/M16b — AI conflict resolution via a headless AI CLI agent. */
  aiEnabled: boolean;
  /** Preset id the UI last applied: "claude" | "gemini" | "codex" | "aider" | "custom". */
  aiProvider: string;
  /** Executable name or full path (e.g. "claude", "gemini", "...\\codex.cmd"). */
  aiCommand: string;
  /** Args template; `{model}` and (when promptVia="arg") `{prompt}` placeholders. */
  aiArgs: string;
  /** "" = the tool's default model; else a provider-specific alias/id. */
  aiModel: string;
  /** "stdin" = prompt fed via STDIN; "arg" = prompt embedded in args via {prompt}. */
  aiPromptVia: "stdin" | "arg";
  /** "claude-json" = parse Claude's JSON envelope; "none" = exit-code only. */
  aiOutputFormat: "claude-json" | "none";
  aiTimeoutSecs: number;
  /** Per-repo forge connection metadata. Tokens are NOT here — they live in the OS keychain. */
  forgeConnections?: Record<string, ForgeConnection>;
}

/** Result of `detect_ai_tool` Tauri command. */
export interface DetectedAi {
  found: boolean;
  path: string;
  version: string;
}

/** Result of `run_ai_resolve` Tauri command. */
export interface AiResult {
  success: boolean;
  isError: boolean;
  resultText: string;
  error: string;
  costUsd: number;
  durationMs: number;
}

export interface ExtractDiffFilesResult {
  leftPath: string;
  rightPath: string;
  leftLabel: string;
  rightLabel: string;
  tmpDir: string;
}

export interface ExtractConflictFilesResult {
  basePath: string;
  oursPath: string;
  theirsPath: string;
  outputPath: string;
  tmpDir: string;
}

export interface DetectedTool {
  name: string;
  path: string;
}

export interface RpcError {
  code: number;
  message: string;
  data?: Record<string, unknown>;
}

export interface RpcResponse<T = unknown> {
  jsonrpc: string;
  id?: unknown;
  result?: T;
  error?: RpcError;
}
