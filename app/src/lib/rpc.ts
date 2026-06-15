import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import type {
  RpcResponse,
  RpcError,
  OpenRepoResult,
  RepoStatus,
  Branch,
  Remote,
  Commit,
  CompareResult,
  CommitFilter,
  CherryPickResult,
  CherryPickProgress,
  CreateBranchResult,
  FetchResult,
  PullResult,
  PushResult,
  RecentRepo,
  CommitDetail,
  CommitFile,
  DryRunResult,
  FileDiffResult,
  ConflictFilesResult,
  ContinueCherryResult,
  FileContentResult,
  WriteAndStageResult,
  AppSettings,
  ExtractDiffFilesResult,
  ExtractConflictFilesResult,
  ForgeKind,
  ForgeConnectionTest,
  CreatePRResult,
  PRSummary,
} from "./rpc-types";

export class RpcCallError extends Error {
  constructor(public readonly rpcError: RpcError) {
    super(`[${rpcError.code}] ${rpcError.message}`);
  }
}

async function call<T>(method: string, params?: Record<string, unknown>): Promise<T> {
  const res = await invoke<RpcResponse<T>>("sidecar_call", { method, params: params ?? null });
  if (res.error) throw new RpcCallError(res.error);
  return res.result as T;
}

export const rpc = {
  ping: () => call<string>("ping"),

  version: () => call<{ sidecar: string; go: string; git: string }>("version"),

  cancel: () => invoke<void>("sidecar_cancel"),

  recents: {
    load: () => invoke<RecentRepo[]>("recents_load"),
    save: (items: RecentRepo[]) => invoke<void>("recents_save", { recents: items }),
  },

  settings: {
    load: () => invoke<AppSettings>("settings_load"),
    save: (settings: AppSettings) => invoke<void>("settings_save", { settings }),
  },

  git: {
    openRepo: (repo: string) =>
      call<OpenRepoResult>("git.openRepo", { repo }),

    status: (repo: string) =>
      call<RepoStatus>("git.status", { repo }),

    branches: (repo: string, includeRemote = false) =>
      call<Branch[]>("git.branches", { repo, includeRemote }),

    remotes: (repo: string) =>
      call<Remote[]>("git.remotes", { repo }),

    defaultBranch: (repo: string, remote = "origin") =>
      call<string>("git.defaultBranch", { repo, remote }),

    commits: (repo: string, ref = "HEAD", limit = 100, skip = 0, filter?: CommitFilter) =>
      call<Commit[]>("git.commits", { repo, ref, limit, skip, filter }),

    // M12b — commits in `head` but not in `base`, for the "N new commits" badge.
    compareBranches: (repo: string, base: string, head: string, limit?: number) =>
      call<CompareResult>("git.compareBranches", { repo, base, head, limit }),

    cherryPick: async (
      repo: string,
      target: string,
      shas: string[],
      strategy?: "smart" | "theirs" | "ours",
      onprogress?: (p: CherryPickProgress) => void,
      messages?: Record<string, string>,  // M11b — per-SHA message override
    ) => {
      const unlisten = onprogress
        ? await listen<CherryPickProgress>("cp-progress", (e) => onprogress(e.payload))
        : null;
      try {
        return await call<CherryPickResult>("git.cherryPick", { repo, target, shas, strategy, messages });
      } finally {
        unlisten?.();
      }
    },

    abort: (repo: string) => call<void>("git.abort", { repo }),

    createBranch: (repo: string, name: string, base?: string) =>
      call<CreateBranchResult>("git.createBranch", { repo, name, base }),

    fetch: (repo: string, remote = "origin") =>
      call<FetchResult>("git.fetch", { repo, remote }),

    pull: (repo: string, branch: string, remote = "origin") =>
      call<PullResult>("git.pull", { repo, branch, remote }),

    push: (repo: string, branch: string, remote = "origin") =>
      call<PushResult>("git.push", { repo, branch, remote }),

    commitDetail: (repo: string, sha: string) =>
      call<CommitDetail>("git.commitDetail", { repo, sha }),

    commitFiles: (repo: string, sha: string) =>
      call<CommitFile[]>("git.commitFiles", { repo, sha }),

    dryRunPick: (repo: string, target: string, shas: string[]) =>
      call<DryRunResult>("git.dryRunPick", { repo, target, shas }),

    fileDiff: (repo: string, sha: string, file: string) =>
      call<FileDiffResult>("git.fileDiff", { repo, sha, file }),
    stagedFileDiff: (repo: string, file: string) =>
      call<FileDiffResult>("git.stagedFileDiff", { repo, file }),

    conflictFiles: (repo: string) =>
      call<ConflictFilesResult>("git.conflictFiles", { repo }),

    resolveConflict: (repo: string, file: string, strategy: "ours" | "theirs") =>
      call<{ resolved: boolean }>("git.resolveConflict", { repo, file, strategy }),

    continueCherry: (repo: string, message?: string) =>
      call<ContinueCherryResult>("git.continueCherry", { repo, message }),

    // M11b — squash commits added since `base` into one commit with `message`.
    squashCommits: (repo: string, base: string, message: string) =>
      call<{ squashed: boolean }>("git.squashCommits", { repo, base, message }),

    // M11c — apply only `keep` files of commit `sha` onto `target` as a new commit.
    partialPick: (repo: string, target: string, sha: string, keep: string[], message?: string) =>
      call<{ sha: string; kept: string[] }>("git.partialPick", { repo, target, sha, keep, message }),

    fileContent: (repo: string, file: string) =>
      call<FileContentResult>("git.fileContent", { repo, file }),

    writeAndStageFile: (repo: string, file: string, content: string) =>
      call<WriteAndStageResult>("git.writeAndStageFile", { repo, file, content }),

    extractDiffFiles: (repo: string, sha: string, file: string) =>
      call<ExtractDiffFilesResult>("git.extractDiffFiles", { repo, sha, file }),

    extractConflictFiles: (repo: string, file: string) =>
      call<ExtractConflictFilesResult>("git.extractConflictFiles", { repo, file }),

    stageResolvedFile: (repo: string, file: string, contentPath: string) =>
      call<{ staged: boolean }>("git.stageResolvedFile", { repo, file, contentPath }),

    cleanupTmpDir: (tmpDir: string) =>
      call<Record<string, never>>("git.cleanupTmpDir", { tmpDir }),

    cherry: (repo: string, source: string, target: string, maxCount = 0) =>
      call<string[]>("git.cherry", { repo, source, target, maxCount }),

    // M16 — AI conflict resolution helpers
    restoreConflict: (repo: string, file: string) =>
      call<{ restored: boolean }>("git.restoreConflict", { repo, file }),

    diffTexts: (leftText: string, rightText: string) =>
      call<FileDiffResult>("git.diffTexts", { leftText, rightText }),

    // M11a — auto-stash. Stash before a cherry-pick batch, pop after the flow.
    stash: (repo: string, message: string, includeUntracked: boolean) =>
      call<{ stashed: boolean }>("git.stash", { repo, message, includeUntracked }),
    stashPop: (repo: string, message: string) =>
      call<{ popped: boolean }>("git.stashPop", { repo, message }),
  },

  // ── M14 — Forge integration (GitHub/GitLab PR creation) ──────────────────
  // These commands run in Rust (NOT through the sidecar) — Rust holds the
  // token in the OS keychain and makes the HTTP request directly. The token
  // never crosses the IPC boundary back to the frontend after save.
  forge: {
    testConnection: (kind: ForgeKind, baseUrl: string, token: string, username?: string) =>
      invoke<ForgeConnectionTest>("forge_test_connection", { kind, baseUrl, token, username }),

    // M14f — test an already-saved connection (token loaded from keychain in Rust).
    testSavedConnection: (repoPath: string) =>
      invoke<ForgeConnectionTest>("forge_test_saved_connection", { repoPath }),

    saveConnection: (args: {
      repoPath: string;
      kind: ForgeKind;
      baseUrl: string;
      host: string;
      username: string;
      token: string;
    }) => invoke<void>("forge_save_connection", args),

    deleteConnection: (repoPath: string) =>
      invoke<void>("forge_delete_connection", { repoPath }),

    createPR: (args: {
      repoPath: string;
      projectPath: string;
      base: string;
      head: string;
      title: string;
      body: string;
      draft: boolean;
    }) => invoke<CreatePRResult>("forge_create_pr", args),

    listPRs: (args: {
      repoPath: string;
      projectPath: string;
      head: string;
    }) => invoke<PRSummary[]>("forge_list_prs", args),
  },
};
