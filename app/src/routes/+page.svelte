<script lang="ts">
  import { rpc, RpcCallError } from "$lib/rpc";
  import type { Branch, Commit, CommitFilter, CherryPickResult, CherryPickProgress, RecentRepo, CommitDetail, CommitFile, DryRunItem, ConflictFileInfo, AppSettings, Remote, ForgeConnection, PRSummary, AiResult, FileDiffResult } from "$lib/rpc-types";
  import { parseForge } from "$lib/forge";
  import { AI_PROVIDERS, renderAiArgs } from "$lib/ai-providers";
  import { invoke } from "@tauri-apps/api/core";
  import { WebviewWindow } from "@tauri-apps/api/webviewWindow";
  import { listen } from "@tauri-apps/api/event";
  import { check, type Update } from "@tauri-apps/plugin-updater";
  import { relaunch } from "@tauri-apps/plugin-process";
  import { openUrl } from "@tauri-apps/plugin-opener";
  import Toolbar from "$lib/Toolbar.svelte";
  import CommitList from "$lib/CommitList.svelte";
  import PickQueue from "$lib/PickQueue.svelte";
  import ResultBanner from "$lib/ResultBanner.svelte";
  import UpdateBanner from "$lib/UpdateBanner.svelte";
  import CommitDetailPanel from "$lib/CommitDetail.svelte";
  import CommitFilesPanel from "$lib/CommitFiles.svelte";
  import ConflictResolver from "$lib/ConflictResolver.svelte";
  import SettingsModal from "$lib/Settings.svelte";
  import GitConsole from "$lib/GitConsole.svelte";
  import Toast, { type ToastItem } from "$lib/Toast.svelte";
  import ConnectForge from "$lib/ConnectForge.svelte";
  import CreatePR from "$lib/CreatePR.svelte";
  import AiReviewModal from "$lib/AiReviewModal.svelte";

  // ── settings ──────────────────────────────────────────────
  const DEFAULT_SETTINGS: AppSettings = {
    maxCommits: 100, defaultApplyMode: "apply", showEolMarkers: false, autoFetchOnOpen: false, theme: "dark",
    externalDiffEnabled: false, externalDiffPath: "", externalDiffArgs: "",
    externalMergeEnabled: false, externalMergePath: "", externalMergeArgs: "",
    checkForUpdatesOnStartup: true, autoStash: false,
    aiEnabled: false, aiProvider: "claude", aiCommand: "", aiArgs: AI_PROVIDERS[0].args,
    aiModel: "", aiPromptVia: "stdin", aiOutputFormat: "claude-json", aiTimeoutSecs: 120,
  };
  let settings = $state<AppSettings>(DEFAULT_SETTINGS);
  // M16/M16b — AI conflict resolution available when an executable is configured.
  const aiAvailable = $derived(settings.aiEnabled && !!settings.aiCommand);
  let settingsOpen = $state(false);
  let pendingUpdate = $state<Update | null>(null);
  let updateDownloading = $state(false);
  let updateProgress = $state(0);
  let consoleOpen = $state(false);
  let consoleHeight = $state(180);

  // ── toast notifications ───────────────────────────────────
  let toasts = $state<ToastItem[]>([]);
  let toastId = 0;

  function showToast(type: ToastItem["type"], message: string, detail?: string) {
    const id = ++toastId;
    toasts = [...toasts, { id, type, message, detail }];
    if (type !== "error") {
      setTimeout(() => { toasts = toasts.filter(t => t.id !== id); }, type === "success" ? 5000 : 8000);
    }
  }

  function dismissToast(id: number) {
    toasts = toasts.filter(t => t.id !== id);
  }

  // ── undo stack ────────────────────────────────────────────
  let undoStack = $state<Map<string, Commit>[]>([]);
  const MAX_UNDO = 20;

  function pushUndo() {
    undoStack = [...undoStack.slice(-(MAX_UNDO - 1)), new Map(selectionMap)];
  }

  function undo() {
    if (undoStack.length === 0) return;
    selectionMap = new Map(undoStack[undoStack.length - 1]);
    undoStack = undoStack.slice(0, -1);
    scheduleDryRun();
  }

  function startConsoleResize(e: MouseEvent) {
    e.preventDefault();
    const startY = e.clientY;
    const startH = consoleHeight;
    function onMove(ev: MouseEvent) {
      consoleHeight = Math.max(80, Math.min(600, startH + (startY - ev.clientY)));
    }
    function onUp() {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }

  rpc.settings.load().then(s => {
    settings = s;
    if (s.checkForUpdatesOnStartup) {
      check().then(u => { if (u) pendingUpdate = u; }).catch(() => {});
    }
  }).catch(() => {});

  async function installUpdate() {
    if (!pendingUpdate) return;
    updateDownloading = true;
    updateProgress = 0;
    let totalBytes = 0;
    let downloadedBytes = 0;
    try {
      await pendingUpdate.downloadAndInstall((event: any) => {
        if (event.event === "Started") {
          totalBytes = event.data.contentLength ?? 0;
        } else if (event.event === "Progress") {
          downloadedBytes += event.data.chunkLength;
          if (totalBytes > 0) updateProgress = (downloadedBytes / totalBytes) * 100;
        }
      });
      await relaunch();
    } catch {
      updateDownloading = false;
      updateProgress = 0;
    }
  }

  async function checkForUpdates() {
    const u = await check().catch(() => null);
    if (u) pendingUpdate = u;
    return !!u;
  }

  $effect(() => {
    document.body.classList.toggle("light", settings.theme === "light");
  });

  async function saveSettings(s: AppSettings) {
    settings = s;
    try { await rpc.settings.save(s); } catch { /* ignore */ }
  }

  // ── repo state ────────────────────────────────────────────
  let repoPath = $state("");
  let currentBranch = $state("");

  // ── branch / commit lists ─────────────────────────────────
  let branches = $state<Branch[]>([]);
  let remotes = $state<Remote[]>([]);
  /** Default branch (from `origin/HEAD` or convention) — used as PR base default. */
  let defaultBranch = $state("");

  // ── M14 forge connection ─────────────────────────────────
  let forgeConnection = $state<ForgeConnection | null>(null);
  let connectForgeOpen = $state(false);
  // M14f — which repo the ConnectForge dialog targets (may differ from the open repo).
  let connectForgeRepo = $state("");
  let connectForgeRemotes = $state<Remote[]>([]);
  let createPrOpen = $state(false);
  /** Snapshot of commits just applied — passed to CreatePR for title/body. */
  let lastAppliedCommits = $state<Commit[]>([]);
  /** Remote we pushed to — passed to CreatePR to compute project_path. */
  let lastPushRemote = $state("");
  /** Survives conflict resolution: what to do after all queued commits land.
   * Set by applyPick/applyPushCreatePR. Consumed by both the success path of
   * applyPickShas and the success path of continueCherry. */
  let pendingFinalize = $state<{ remote: string; createPr: boolean } | null>(null);
  /** M11a — true when applyPick auto-stashed uncommitted work; survives conflict
   * resolution so the stash is popped only once the whole flow ends. */
  let pendingAutoStashPop = $state(false);
  const AUTO_STASH_MSG = "lcp-autostash";
  // ── M11b — advanced pick: squash + per-commit message override ──
  let squashMode = $state(false);
  let squashMessage = $state("");
  let messageOverrides = $state(new Map<string, string>());
  let squashBase = $state(""); // target tip SHA captured before a squash apply
  let partialPicking = $state(false); // M11c — partial-file pick in flight
  /** Open PRs for the current target branch — used to show "PR #N open" status
   * in PickQueue so the user knows if there's already a PR before they try to create one. */
  let targetBranchPRs = $state<PRSummary[]>([]);
  let prCheckLoading = $state(false);
  const forgeConnected = $derived(forgeConnection !== null);
  let sourceBranch = $state("");
  let targetBranch = $state("");
  let commits = $state<Commit[]>([]);
  let loadingCommits = $state(false);

  // ── selection: Map preserves insertion order for queue ─────
  let selectionMap = $state(new Map<string, Commit>());
  const queue = $derived([...selectionMap.values()]);
  /** Squash only makes sense for ≥2 commits — a stale `squashMode=true` left
   * over from a previous larger batch must not silently squash/rename a
   * single-commit apply. Single source of truth for both UI (label, ✎
   * visibility) and backend decisions (message overrides, squash capture). */
  const squashActive = $derived(squashMode && queue.length > 1);

  // ── applied commits (already in target branch) ────────────
  let appliedShas = $state(new Set<string>());

  async function refreshApplied() {
    if (!repoPath || !sourceBranch || !targetBranch || sourceBranch === targetBranch) {
      appliedShas = new Set();
      return;
    }
    try {
      const shas = await rpc.git.cherry(repoPath, sourceBranch, targetBranch, settings.maxCommits);
      appliedShas = new Set(shas);
    } catch {
      appliedShas = new Set();
    }
  }

  // ── refresh (fetch / pull) ────────────────────────────────
  let refreshing = $state(false);

  async function doFetch() {
    if (!repoPath) return;
    refreshing = true;
    try {
      await rpc.git.fetch(repoPath);
      branches = await rpc.git.branches(repoPath, true);
      await loadCommits(sourceBranch);
    } catch (e) {
      showToast("error", e instanceof RpcCallError ? e.rpcError.message : String(e));
    } finally {
      refreshing = false;
    }
  }

  async function doPull() {
    if (!repoPath) return;
    refreshing = true;
    try {
      await rpc.git.pull(repoPath, sourceBranch);
      branches = await rpc.git.branches(repoPath, true);
      await loadCommits(sourceBranch);
    } catch (e) {
      showToast("error", e instanceof RpcCallError ? e.rpcError.message : String(e));
    } finally {
      refreshing = false;
    }
  }

  // ── apply / progress ──────────────────────────────────────
  let busy = $state(false);
  let progress = $state<CherryPickProgress | null>(null);
  let applyResult = $state<CherryPickResult | null>(null);
  let applyError = $state("");

  // ── conflict resolver (M5c) ──────────────────────────────
  let conflictFiles = $state<ConflictFileInfo[]>([]);
  let conflictSha = $state("");
  let resolvedSet = $state(new Set<string>());
  let conflictBusy = $state(false);
  // Files touched by each remaining queued commit (fetched when conflict mode enters)
  let remainingCommitFiles = $state<Map<string, string[]>>(new Map());

  async function loadConflictFiles(): Promise<boolean> {
    try {
      const r = await rpc.git.conflictFiles(repoPath);
      conflictFiles = r.files;
      resolvedSet = new Set();
      return true;
    } catch {
      return false; // don't touch applyError — caller decides how to surface this
    }
  }

  // Fetch which files each remaining queue commit touches, so the UI can show them.
  async function loadRemainingCommitFiles(conflictingSha: string) {
    const idx = queue.findIndex(c =>
      c.sha === conflictingSha || c.sha.startsWith(conflictingSha) || conflictingSha.startsWith(c.sha)
    );
    const remaining = idx >= 0 ? queue.slice(idx + 1) : [];
    if (remaining.length === 0) { remainingCommitFiles = new Map(); return; }
    const m = new Map<string, string[]>();
    await Promise.all(remaining.map(async c => {
      try {
        const r = await rpc.git.commitFiles(repoPath, c.sha);
        m.set(c.sha, r.map(f => f.path));
      } catch { /* best-effort */ }
    }));
    remainingCommitFiles = m;
  }

  async function resolveConflictFile(file: string, strategy: "ours" | "theirs") {
    conflictBusy = true;
    try {
      await rpc.git.resolveConflict(repoPath, file, strategy);
      resolvedSet = new Set([...resolvedSet, file]);
    } catch (e) {
      applyError = e instanceof RpcCallError ? e.rpcError.message : String(e);
    } finally {
      conflictBusy = false;
    }
  }

  async function continueCherry() {
    conflictBusy = true;
    try {
      // M11b — amend the resolved commit with its message override (unless squashing).
      const msgOverride = squashActive ? undefined : messageOverrides.get(conflictSha);
      await rpc.git.continueCherry(repoPath, msgOverride);
      // The resolved commit is now committed. Find remaining queue commits to apply next.
      // (Sidecar applies one commit at a time — git's sequencer has no knowledge of the rest.)
      const resolvedSha = conflictSha;
      const resolvedIdx = queue.findIndex(c =>
        c.sha === resolvedSha || c.sha.startsWith(resolvedSha) || resolvedSha.startsWith(c.sha)
      );
      const remainingShas = resolvedIdx >= 0 ? queue.slice(resolvedIdx + 1).map(c => c.sha) : [];

      // Clear conflict state
      conflictFiles = [];
      resolvedSet = new Set();
      remainingCommitFiles = new Map();
      conflictSha = "";
      applyError = "";
      applyResult = null;
      aiResolved = new Map();
      aiBackup = new Map();
      aiReviewOpen = false;
      branches = await rpc.git.branches(repoPath, true);
      const updated = branches.find((b) => b.isHead);
      if (updated) currentBranch = updated.name;

      // Apply any remaining queued commits (may produce new conflicts).
      // pendingFinalize carries the push/PR intent through this recursion.
      if (remainingShas.length > 0) {
        conflictBusy = false;
        await applyPickShas(remainingShas);
        return;
      }
      // All commits applied — clear the queue selection
      selectionMap = new Map();
      undoStack = [];
      refreshApplied();
      // If user originally picked "Apply & Push" or "Apply & Push & Create PR",
      // finish that intent now. nApplied isn't precisely known here (the resolved
      // commit just landed), so we pass 1 for messaging — accurate count survives
      // in pickResult.applied when no conflict, but here we only know the just-resolved one.
      if (pendingFinalize) {
        await finalizeAfterApply(1, pendingFinalize);
      } else {
        showToast("success", `Conflict resolved — cherry-pick applied to ${targetBranch}`);
      }
      // Whole flow finished via conflict resolution — squash (if on) then restore stash.
      await finalizeBatchExtras();
    } catch (e) {
      // git cherry-pick --continue failed (e.g. unstaged files, or genuine error)
      applyError = e instanceof RpcCallError ? e.rpcError.message : String(e);
      await loadConflictFiles();
      if (conflictFiles.length === 0) {
        conflictSha = "";
        remainingCommitFiles = new Map();
      } else {
        try {
          const r = await rpc.git.openRepo(repoPath);
          if (r.cherryPickHead) conflictSha = r.cherryPickHead;
        } catch { /* ignore */ }
        await loadRemainingCommitFiles(conflictSha);
      }
    } finally {
      conflictBusy = false;
    }
  }

  async function viewConflictFile(file: string) {
    if (!repoPath) return;

    if (settings.externalMergeEnabled && settings.externalMergePath) {
      conflictBusy = true;
      try {
        const res = await rpc.git.extractConflictFiles(repoPath, file);
        const template = settings.externalMergeArgs || '"{theirs}" "{ours}" "{base}" "{output}"';
        const args = buildArgs(template, {
          base: res.basePath, ours: res.oursPath,
          theirs: res.theirsPath, output: res.outputPath,
        });
        await invoke("launch_and_wait", { program: settings.externalMergePath, args });
        await rpc.git.stageResolvedFile(repoPath, file, res.outputPath);
        await rpc.git.cleanupTmpDir(res.tmpDir);
        resolvedSet = new Set([...resolvedSet, file]);
      } catch (e) {
        applyError = e instanceof RpcCallError ? e.rpcError.message : String(e);
      } finally {
        conflictBusy = false;
      }
      return;
    }

    const params = new URLSearchParams({ repo: repoPath, file });
    new WebviewWindow(`conflict-${Date.now()}`, {
      url: `${window.location.origin}/conflict?${params}`,
      title: `Conflict: ${file}`,
      width: 800,
      height: 600,
    });
  }

  function viewConflictFileDiff(file: string) {
    if (!repoPath) return;
    const params = new URLSearchParams({ repo: repoPath, file, staged: "true", status: "M" });
    new WebviewWindow(`diff-${Date.now()}`, {
      url: `${window.location.origin}/diff?${params}`,
      title: `Staged diff: ${file}`,
      width: 900,
      height: 650,
    });
  }

  async function abortConflict() {
    conflictBusy = true;
    try { await rpc.git.abort(repoPath); } catch { /* ignore */ }
    conflictFiles = [];
    conflictSha = "";
    resolvedSet = new Set();
    applyResult = null;
    applyError = "";
    pendingFinalize = null; // user gave up — don't push/PR on next action
    squashBase = ""; // don't squash a partially-applied/aborted batch
    aiResolved = new Map();
    aiBackup = new Map();
    aiReviewOpen = false;
    conflictBusy = false;
    await maybePopAutoStash(); // restore the user's stashed work after aborting
    showToast("warning", "Cherry-pick aborted.");
  }

  // ── M16/M16b: AI conflict resolution (headless AI CLI agent) ─────────
  let aiBusy = $state(false);
  let aiBackup = $state(new Map<string, string>());   // file → original content (with markers)
  let aiResolved = $state(new Map<string, string>()); // file → AI-merged content, pending review
  let aiReviewOpen = $state(false);
  let aiReviewFile = $state("");
  let aiReviewDiff = $state<FileDiffResult | null>(null);

  function buildConflictPrompt(files: string[]): string {
    const cc = queue.find(c =>
      c.sha === conflictSha || c.sha.startsWith(conflictSha) || conflictSha.startsWith(c.sha)
    );
    const subject = cc?.subject ?? "";
    const fileList = files.map(f => `- ${f}`).join("\n");
    return [
      "You are resolving git merge conflicts left by a cherry-pick.",
      subject ? `The commit being applied (\"theirs\") is: "${subject}".` : "",
      "",
      "Files currently in conflict:",
      fileList,
      "",
      "For EACH file above:",
      "1. Read it and locate every conflict region (<<<<<<< ours / ======= / >>>>>>> theirs).",
      "2. Produce a correct, intelligent merge that keeps BOTH sides' intent where possible — do not blindly pick one side unless the other is clearly redundant.",
      "3. Overwrite the file with the fully merged result, removing ALL conflict markers (<<<<<<<, =======, >>>>>>>).",
      "",
      "Constraints:",
      "- Edit ONLY the files listed above.",
      "- Do NOT run git (add/commit/cherry-pick/push/abort) or any shell command — you have no shell access.",
      "- Do not leave any conflict markers behind.",
    ].filter(Boolean).join("\n");
  }

  async function aiResolveAll() {
    if (!repoPath || aiBusy) return;
    const files = conflictFiles
      .filter(f => !resolvedSet.has(f.path) && !aiResolved.has(f.path))
      .map(f => f.path);
    if (files.length === 0) return;
    aiBusy = true;
    try {
      // Backup originals (with markers) so a discarded resolution can be undone.
      const backup = new Map(aiBackup);
      for (const f of files) {
        try { backup.set(f, (await rpc.git.fileContent(repoPath, f)).content); } catch { /* best-effort */ }
      }
      aiBackup = backup;

      const prompt = buildConflictPrompt(files);
      const args = renderAiArgs(settings.aiArgs, settings.aiModel, prompt, settings.aiPromptVia);
      const result = await invoke<AiResult>("run_ai_resolve", {
        repoPath,
        command: settings.aiCommand,
        args,
        prompt,
        promptVia: settings.aiPromptVia,
        outputFormat: settings.aiOutputFormat,
        timeoutSecs: settings.aiTimeoutSecs,
      });

      // Read each file back from disk; accept only those with no markers left.
      const resolved = new Map(aiResolved);
      const failed: string[] = [];
      for (const f of files) {
        try {
          const c = (await rpc.git.fileContent(repoPath, f)).content;
          if (c.includes("<<<<<<<") || c.includes(">>>>>>>")) failed.push(f);
          else resolved.set(f, c);
        } catch { failed.push(f); }
      }
      aiResolved = resolved;

      const okCount = files.length - failed.length;
      const costStr = result.costUsd ? ` ($${result.costUsd.toFixed(3)})` : "";
      if (okCount > 0) {
        showToast("success", `AI resolved ${okCount} file${okCount === 1 ? "" : "s"} — review before staging${costStr}`);
      }
      if (failed.length > 0) {
        const detail = result.isError && result.error ? result.error : failed.join(", ");
        showToast("warning", `AI could not fully resolve ${failed.length} file${failed.length === 1 ? "" : "s"} — resolve manually`, detail);
      }
    } catch (e) {
      showToast("error", "AI resolve failed", typeof e === "string" ? e : (e instanceof RpcCallError ? e.rpcError.message : String(e)));
    } finally {
      aiBusy = false;
    }
  }

  async function aiReview(file: string) {
    if (!repoPath) return;

    // If an external merge tool is configured, reuse the M8 pipeline: the working-tree
    // file already holds the AI's marker-free merge (Claude wrote it directly, with
    // Bash disallowed so `git add` never ran) — ExtractConflictFiles copies that as
    // "output" alongside base/ours/theirs from the still-unmerged index stages, giving
    // a familiar 4-way view in the user's own tool. Closing the tool stages the result,
    // same as the manual conflict flow.
    if (settings.externalMergeEnabled && settings.externalMergePath) {
      conflictBusy = true;
      try {
        const res = await rpc.git.extractConflictFiles(repoPath, file);
        const template = settings.externalMergeArgs || '"{theirs}" "{ours}" "{base}" "{output}"';
        const args = buildArgs(template, {
          base: res.basePath, ours: res.oursPath,
          theirs: res.theirsPath, output: res.outputPath,
        });
        await invoke("launch_and_wait", { program: settings.externalMergePath, args });
        await rpc.git.stageResolvedFile(repoPath, file, res.outputPath);
        await rpc.git.cleanupTmpDir(res.tmpDir);
        resolvedSet = new Set([...resolvedSet, file]);
        const m = new Map(aiResolved); m.delete(file); aiResolved = m;
      } catch (e) {
        showToast("error", "External merge review failed", e instanceof RpcCallError ? e.rpcError.message : String(e));
      } finally {
        conflictBusy = false;
      }
      return;
    }

    aiReviewFile = file;
    aiReviewDiff = null;
    aiReviewOpen = true;
    try {
      aiReviewDiff = await rpc.git.diffTexts(aiBackup.get(file) ?? "", aiResolved.get(file) ?? "");
    } catch (e) {
      showToast("error", "Cannot build review diff", e instanceof RpcCallError ? e.rpcError.message : String(e));
      aiReviewOpen = false;
    }
  }

  async function aiAccept(file: string) {
    if (!repoPath) return;
    const content = aiResolved.get(file);
    if (content === undefined) return;
    conflictBusy = true;
    try {
      await rpc.git.writeAndStageFile(repoPath, file, content);
      resolvedSet = new Set([...resolvedSet, file]);
      const m = new Map(aiResolved); m.delete(file); aiResolved = m;
      if (aiReviewOpen && aiReviewFile === file) aiReviewOpen = false;
    } catch (e) {
      showToast("error", "Stage failed", e instanceof RpcCallError ? e.rpcError.message : String(e));
    } finally {
      conflictBusy = false;
    }
  }

  async function aiDiscard(file: string) {
    if (!repoPath) return;
    conflictBusy = true;
    try {
      await rpc.git.restoreConflict(repoPath, file);
    } catch (e) {
      showToast("error", "Restore failed", e instanceof RpcCallError ? e.rpcError.message : String(e));
    } finally {
      const m = new Map(aiResolved); m.delete(file); aiResolved = m;
      const b = new Map(aiBackup); b.delete(file); aiBackup = b;
      if (aiReviewOpen && aiReviewFile === file) aiReviewOpen = false;
      conflictBusy = false;
    }
  }

  // ── commit detail (M5a) ───────────────────────────────────
  let selectedCommit = $state<Commit | null>(null);
  let commitDetail = $state<CommitDetail | null>(null);
  let commitFiles = $state<CommitFile[]>([]);
  let loadingDetail = $state(false);
  let detailError = $state("");
  let detailHeight = $state(200);

  async function selectCommit(commit: Commit) {
    if (selectedCommit?.sha === commit.sha) {
      selectedCommit = null;
      commitDetail = null;
      commitFiles = [];
      detailError = "";
      return;
    }
    selectedCommit = commit;
    commitDetail = null;
    commitFiles = [];
    detailError = "";
    selectedFile = null;
    if (!repoPath) return;
    loadingDetail = true;
    try {
      // Sequential — Rust ActiveSidecar only tracks one child at a time.
      commitDetail = await rpc.git.commitDetail(repoPath, commit.sha);
      commitFiles = await rpc.git.commitFiles(repoPath, commit.sha);
    } catch (e) {
      detailError = e instanceof RpcCallError ? e.rpcError.message : String(e);
    } finally {
      loadingDetail = false;
    }
  }

  // ── external tool helpers ─────────────────────────────────

  // Parse an args template with {placeholder} substitution into a string[].
  function buildArgs(template: string, vars: Record<string, string>): string[] {
    let s = template;
    for (const [k, v] of Object.entries(vars)) s = s.replaceAll(`{${k}}`, v);
    const result: string[] = [];
    // Match prefix+"quoted value" (e.g. /path:"C:\some path\file") as ONE arg,
    // or a bare quoted string, or a non-space token.
    // Note: [^\s"] (not \S) for the prefix — \S includes " which breaks the parse.
    const re = /([^\s"]*)"([^"]*)"|\S+/g;
    let m: RegExpExecArray | null;
    while ((m = re.exec(s)) !== null) result.push(m[2] !== undefined ? m[1] + m[2] : m[0]);
    return result;
  }

  // ── file diff viewer ─────────────────────────────────────
  let selectedFile = $state<CommitFile | null>(null);

  async function selectFile(file: CommitFile) {
    if (!selectedCommit || !repoPath) return;
    selectedFile = file;

    if (settings.externalDiffEnabled && settings.externalDiffPath) {
      try {
        const res = await rpc.git.extractDiffFiles(repoPath, selectedCommit.sha, file.path);
        const template = settings.externalDiffArgs || '"{left}" "{right}"';
        const args = buildArgs(template, {
          left: res.leftPath, right: res.rightPath,
          leftLabel: res.leftLabel, rightLabel: res.rightLabel,
        });
        await invoke("launch_detached", { program: settings.externalDiffPath, args });
        return;
      } catch (e) {
        showToast("error", `External diff tool error: ${e instanceof Error ? e.message : String(e)}`);
        return;
      }
    }

    const params = new URLSearchParams({
      repo: repoPath,
      sha: selectedCommit.sha,
      file: file.path,
      status: file.status,
      added: String(file.added),
      removed: String(file.removed),
    });
    new WebviewWindow(`diff-${Date.now()}`, {
      url: `${window.location.origin}/diff?${params}`,
      title: `Diff: ${file.path}`,
      width: 900,
      height: 650,
    });
  }

  function startDetailResize(e: MouseEvent) {
    e.preventDefault();
    const startY = e.clientY;
    const startH = detailHeight;
    function onMove(ev: MouseEvent) {
      detailHeight = Math.max(80, Math.min(520, startH + (startY - ev.clientY)));
    }
    function onUp() {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }

  // ── column (left / right pane) resize ────────────────────
  let pickQueueWidth = $state(340);

  function startColResize(e: MouseEvent) {
    e.preventDefault();
    const startX = e.clientX;
    const startW = pickQueueWidth;
    function onMove(ev: MouseEvent) {
      pickQueueWidth = Math.max(220, Math.min(600, startW - (ev.clientX - startX)));
    }
    function onUp() {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }

  // ── dry-run conflict preview (M5b) ────────────────────────
  let dryRunMap = $state(new Map<string, DryRunItem>());
  let dryRunTimer: ReturnType<typeof setTimeout> | null = null;

  function scheduleDryRun() {
    if (dryRunTimer) clearTimeout(dryRunTimer);
    dryRunTimer = setTimeout(runDryRun, 400);
  }

  async function runDryRun() {
    if (!repoPath || queue.length === 0) {
      dryRunMap = new Map();
      return;
    }
    const shas = queue.map((c) => c.sha);
    try {
      const res = await rpc.git.dryRunPick(repoPath, targetBranch, shas);
      const m = new Map<string, DryRunItem>();
      for (const item of res.results) m.set(item.sha, item);
      dryRunMap = m;
    } catch { /* silently ignore — dry-run is best-effort */ }
  }

  // ── recent repos ──────────────────────────────────────────
  let recentRepos = $state<RecentRepo[]>([]);

  async function loadRecents() {
    try { recentRepos = await rpc.recents.load(); } catch { /* ignore */ }
  }

  async function saveRecent(path: string) {
    const now = Math.floor(Date.now() / 1000);
    const filtered = recentRepos.filter((r) => r.path !== path);
    recentRepos = [{ path, lastOpened: now }, ...filtered].slice(0, 10);
    try { await rpc.recents.save(recentRepos); } catch { /* ignore */ }
  }

  loadRecents();

  // Listen for conflict-file-resolved events emitted by the conflict merge editor window.
  $effect(() => {
    let unlisten: (() => void) | null = null;
    listen<{ file: string }>("conflict-file-resolved", (e) => {
      resolvedSet = new Set([...resolvedSet, e.payload.file]);
    }).then(fn => { unlisten = fn; });
    return () => { unlisten?.(); };
  });

  // Auto-refresh open-PR list for the target branch whenever the inputs change.
  // Best-effort — silently leaves the list empty on connection / API issues.
  $effect(() => {
    // Touch the reactive deps so Svelte tracks them
    void forgeConnection; void repoPath; void targetBranch; void remotes;
    refreshTargetPRs();
  });

  // ── open repo ─────────────────────────────────────────────
  async function openRepo(path: string) {
    applyResult = null;
    applyError = "";
    selectionMap = new Map();
    loadingCommits = true;
    try {
      // Fetch everything into local vars first — no visible state changes between
      // awaits — so the UI sees only ONE atomic transition at the end.
      const r = await rpc.git.openRepo(path);
      const [branchList, remoteList, detectedDefault] = await Promise.all([
        rpc.git.branches(r.path, true),
        rpc.git.remotes(r.path).catch(() => [] as Remote[]),
        rpc.git.defaultBranch(r.path).catch(() => ""),
      ]);
      const nonCurrent = branchList.find((b) => !b.isHead);
      const newSource = nonCurrent?.name ?? r.branch;
      const newTarget = r.branch;
      const canCherry = newSource !== newTarget;
      const [newCommits, appliedList] = await Promise.all([
        rpc.git.commits(r.path, newSource, settings.maxCommits, 0, {}),
        canCherry
          ? rpc.git.cherry(r.path, newSource, newTarget, settings.maxCommits).catch(() => [] as string[])
          : Promise.resolve([] as string[]),
      ]);

      // Atomic state swap — Svelte batches synchronous assignments into one render.
      repoPath = r.path;
      currentBranch = r.branch;
      branches = branchList;
      remotes = remoteList;
      defaultBranch = detectedDefault;
      sourceBranch = newSource;
      targetBranch = newTarget;
      commits = newCommits;
      appliedShas = new Set(appliedList);
      forgeConnection = settings.forgeConnections?.[r.path] ?? null;
      loadingCommits = false;

      await saveRecent(r.path);
      if (settings.autoFetchOnOpen) {
        // Background refresh after initial load — do NOT show loading state.
        // Fetch + reload commits + cherry atomically so the user sees at most
        // one quiet swap if remote had new commits.
        try {
          await rpc.git.fetch(r.path);
          const canCherryRefresh = newSource !== newTarget;
          const [refreshedBranches, refreshedCommits, refreshedApplied] = await Promise.all([
            rpc.git.branches(r.path, true),
            rpc.git.commits(r.path, newSource, settings.maxCommits, 0, {}),
            canCherryRefresh
              ? rpc.git.cherry(r.path, newSource, newTarget, settings.maxCommits).catch(() => [] as string[])
              : Promise.resolve([] as string[]),
          ]);
          branches = refreshedBranches;
          commits = refreshedCommits;
          appliedShas = new Set(refreshedApplied);
        } catch { /* ignore */ }
      }
      if (r.cherryPickHead) {
        conflictSha = r.cherryPickHead;
        await loadConflictFiles();
        await loadRemainingCommitFiles(r.cherryPickHead);
        // If sidecar failed, conflictSha is still set — ConflictResolver won't show
        // but user can abort. Error shown via applyError.
      }
    } catch (e) {
      applyError = e instanceof RpcCallError ? e.rpcError.message : String(e);
      loadingCommits = false;
    }
  }

  let activeFilter = $state<CommitFilter>({});

  async function loadCommits(branch: string, filter?: CommitFilter) {
    loadingCommits = true;
    commits = [];
    try {
      commits = await rpc.git.commits(repoPath, branch, settings.maxCommits, 0, filter ?? activeFilter);
    } catch (e) {
      applyError = e instanceof RpcCallError ? e.rpcError.message : String(e);
    } finally {
      loadingCommits = false;
    }
  }

  async function changeSourceBranch(branch: string) {
    sourceBranch = branch;
    selectionMap = new Map();
    applyResult = null;
    applyError = "";
    selectedCommit = null;
    commitDetail = null;
    commitFiles = [];
    dryRunMap = new Map();
    activeFilter = {};
    // Keep old commits visible while loading; only set the loading flag so a
    // small indicator can show. Atomic swap below avoids the bright→dim flash.
    loadingCommits = true;
    try {
      const canCherry = repoPath && targetBranch && branch !== targetBranch;
      const [newCommits, appliedList] = await Promise.all([
        rpc.git.commits(repoPath, branch, settings.maxCommits, 0, {}),
        canCherry
          ? rpc.git.cherry(repoPath, branch, targetBranch, settings.maxCommits).catch(() => [] as string[])
          : Promise.resolve([] as string[]),
      ]);
      commits = newCommits;
      appliedShas = new Set(appliedList);
    } catch (e) {
      applyError = e instanceof RpcCallError ? e.rpcError.message : String(e);
    } finally {
      loadingCommits = false;
    }
  }

  function applyCommitFilter(filter: CommitFilter) {
    activeFilter = filter;
    loadCommits(sourceBranch, filter);
  }

  function toggleCommit(sha: string) {
    pushUndo();
    const next = new Map(selectionMap);
    if (next.has(sha)) {
      next.delete(sha);
    } else {
      const c = commits.find((c) => c.sha === sha);
      if (c) next.set(sha, c);
    }
    selectionMap = next;
    scheduleDryRun();
  }

  function removeFromQueue(sha: string) {
    pushUndo();
    const next = new Map(selectionMap);
    next.delete(sha);
    selectionMap = next;
    scheduleDryRun();
  }

  function reorderQueue(from: number, to: number) {
    pushUndo();
    const arr = [...selectionMap.values()];
    const [item] = arr.splice(from, 1);
    arr.splice(to, 0, item);
    selectionMap = new Map(arr.map(c => [c.sha, c]));
    scheduleDryRun();
  }

  async function createBranch(name: string, base: string) {
    if (!repoPath) return;
    try {
      await rpc.git.createBranch(repoPath, name, base);
      branches = await rpc.git.branches(repoPath, true);
      targetBranch = name;
      showToast("success", `Branch "${name}" created from "${base}"`);
    } catch (e) {
      showToast("error", e instanceof RpcCallError ? e.rpcError.message : String(e));
    }
  }

  async function cancelPick() {
    try { await rpc.cancel(); } catch { /* ignore */ }
    try { await rpc.git.abort(repoPath); } catch { /* ignore */ }
    busy = false;
    progress = null;
    pendingFinalize = null;
    squashBase = ""; // don't squash a cancelled batch
    await maybePopAutoStash(); // restore the user's stashed work after cancelling
    showToast("warning", "Cherry-pick cancelled.");
  }

  /** Finalize after a successful cherry-pick batch: optionally push, optionally
   * open the Create PR dialog, and clear `pendingFinalize`. Called both from
   * applyPickShas success and continueCherry success so the push/PR intent
   * survives across conflict resolution. */
  async function finalizeAfterApply(
    nApplied: number,
    intent: { remote: string; createPr: boolean } | null,
  ) {
    if (!intent) {
      showToast("success", `Applied ${nApplied} commit${nApplied === 1 ? "" : "s"} → ${targetBranch}`);
      pendingFinalize = null;
      return;
    }
    // Clear pendingFinalize early so any nested call (e.g. error → retry) doesn't double-execute.
    pendingFinalize = null;
    try {
      await rpc.git.push(repoPath, targetBranch, intent.remote);
      showToast("success", `Applied & pushed ${nApplied} commit${nApplied === 1 ? "" : "s"} → ${intent.remote}/${targetBranch}`);
    } catch (pushErr) {
      showToast("success", `Applied ${nApplied} commit${nApplied === 1 ? "" : "s"} → ${targetBranch}`);
      showToast("error", `Push to ${intent.remote} failed: ${pushErr instanceof RpcCallError ? pushErr.rpcError.message : String(pushErr)}`);
      // Don't open PR if push failed
      return;
    }
    if (intent.createPr) {
      createPrOpen = true;
    }
  }

  // Core apply logic — called by applyPick (full queue) and continueCherry (remaining shas).
  async function applyPickShas(shas: string[], andPush = false, pushRemote = "origin") {
    busy = true;
    progress = null;
    applyResult = null;
    applyError = "";
    try {
      // M11b — pass per-commit message overrides (ignored when squashing).
      const msgRecord = squashActive ? undefined : Object.fromEntries(messageOverrides);
      const pickResult = await rpc.git.cherryPick(
        repoPath, targetBranch, shas, undefined,
        (p) => { progress = p; }, msgRecord
      );
      // success — clear selection, refresh branches
      selectionMap = new Map();
      undoStack = [];
      branches = await rpc.git.branches(repoPath, true);
      const updated = branches.find((b) => b.isHead);
      if (updated) currentBranch = updated.name;

      // await refreshApplied first, then add skipped on top so they persist
      await refreshApplied();
      const skippedShas: string[] = pickResult.skipped ?? [];
      if (skippedShas.length) {
        appliedShas = new Set([...appliedShas, ...skippedShas]);
      }

      const nApplied = pickResult.applied?.length ?? shas.length;
      if (nApplied > 0) {
        // pendingFinalize is the source of truth for push/PR intent — andPush
        // is kept only as a hint from the immediate caller. If the user picked
        // "Apply & Push & Create PR" and we hit a conflict mid-way, the intent
        // survives across continueCherry → applyPickShas recursion via this state.
        const effective = pendingFinalize ?? (andPush ? { remote: pushRemote, createPr: false } : null);
        await finalizeAfterApply(nApplied, effective);
      }
      if (skippedShas.length) {
        const labels = skippedShas.map((sha) => {
          const c = commits.find((x) => x.sha === sha);
          return c ? `${c.subject} (${sha.slice(0, 7)})` : sha.slice(0, 7);
        });
        showToast("warning", `Skipped ${skippedShas.length} commit${skippedShas.length === 1 ? "" : "s"} — already applied`, labels.join("\n"));
      }
      // Whole batch applied with no conflict — squash (if on) then restore stash.
      await finalizeBatchExtras();
    } catch (e) {
      if (e instanceof RpcCallError) {
        if (e.rpcError.code === -32003) {
          const d = e.rpcError.data as { applied?: string[]; conflicts?: { sha: string; files: string[] }[] };
          const conflicts = d?.conflicts ?? [];
          applyResult = { applied: d?.applied ?? [], skipped: [], conflicts };
          if (conflicts.length > 0) {
            conflictSha = conflicts[0].sha;
            const loaded = await loadConflictFiles();
            if (!loaded && conflicts[0].files.length > 0) {
              conflictFiles = conflicts[0].files.map(f => ({ path: f, status: "UU" as const }));
              applyError = "";
            }
            await loadRemainingCommitFiles(conflicts[0].sha);
          }
          // Conflict — keep the stash; it's popped after continue/abort finishes the flow.
        } else {
          showToast("error", `[${e.rpcError.code}] ${e.rpcError.message}`);
          await maybePopAutoStash();
        }
      } else {
        showToast("error", String(e));
        await maybePopAutoStash();
      }
    } finally {
      busy = false;
      progress = null;
    }
  }

  /** M11a — stash uncommitted work before a batch if the setting is on.
   * Returns false only when the stash itself failed (caller should abort). */
  async function maybeAutoStash(): Promise<boolean> {
    pendingAutoStashPop = false;
    if (!settings.autoStash || !repoPath) return true;
    try {
      const r = await rpc.git.stash(repoPath, AUTO_STASH_MSG, true);
      if (r.stashed) {
        pendingAutoStashPop = true;
        showToast("success", "Stashed uncommitted changes — will restore after");
      }
      return true;
    } catch (e) {
      showToast("error", "Auto-stash failed", e instanceof RpcCallError ? e.rpcError.message : String(e));
      return false;
    }
  }

  /** M11a — pop the auto-stash once the whole cherry-pick flow has ended
   * (success or abort). No-op unless this run actually stashed. */
  async function maybePopAutoStash() {
    if (!pendingAutoStashPop || !repoPath) return;
    pendingAutoStashPop = false;
    try {
      const r = await rpc.git.stashPop(repoPath, AUTO_STASH_MSG);
      if (r.popped) showToast("success", "Restored your stashed changes");
    } catch (e) {
      showToast("error", "Couldn't restore stash — resolve, then run `git stash pop` manually",
        e instanceof RpcCallError ? e.rpcError.message : String(e));
    }
  }

  /** M11b — default squash message: each queued commit's subject, one per line. */
  function defaultSquashMessage(): string {
    return queue.map((c) => c.subject).join("\n");
  }

  /** M11b — capture the target branch tip before a squash batch so we can later
   * `reset --soft` back to it. No-op unless squashing is actually active (≥2 commits). */
  async function captureSquashBase() {
    squashBase = "";
    if (!squashActive || !repoPath) return;
    try {
      const cs = await rpc.git.commits(repoPath, targetBranch, 1, 0, {});
      squashBase = cs[0]?.sha ?? "";
    } catch { squashBase = ""; }
  }

  /** M11b — run after a batch fully succeeds (no pending conflict): squash the
   * picked commits into one (if squashMode), then pop the auto-stash. Order
   * matters — squash must finish before the stash is restored. */
  async function finalizeBatchExtras() {
    if (squashMode && squashBase && repoPath) {
      try {
        const r = await rpc.git.squashCommits(repoPath, squashBase, squashMessage.trim() || "Squashed commit");
        if (r.squashed) {
          showToast("success", "Squashed picked commits into one");
          branches = await rpc.git.branches(repoPath, true);
          const updated = branches.find((b) => b.isHead);
          if (updated) currentBranch = updated.name;
          await refreshApplied();
        }
      } catch (e) {
        showToast("error", "Squash failed", e instanceof RpcCallError ? e.rpcError.message : String(e));
      }
    }
    squashBase = "";
    messageOverrides = new Map();
    squashMessage = "";
    await maybePopAutoStash();
  }

  /** M11c — apply only the selected files of the currently-detailed commit
   * onto the target branch as a new commit. Wrapped with auto-stash. */
  async function partialPick(keep: string[]) {
    if (!repoPath || !selectedCommit || keep.length === 0) return;
    partialPicking = true;
    if (!(await maybeAutoStash())) { partialPicking = false; return; }
    try {
      const res = await rpc.git.partialPick(repoPath, targetBranch, selectedCommit.sha, keep);
      showToast("success", `Picked ${res.kept.length} file${res.kept.length === 1 ? "" : "s"} from ${selectedCommit.sha.slice(0, 7)} → ${targetBranch}`);
      branches = await rpc.git.branches(repoPath, true);
      const updated = branches.find((b) => b.isHead);
      if (updated) currentBranch = updated.name;
      await refreshApplied();
    } catch (e) {
      if (e instanceof RpcCallError) showToast("error", `[${e.rpcError.code}] ${e.rpcError.message}`);
      else showToast("error", String(e));
    } finally {
      await maybePopAutoStash();
      partialPicking = false;
    }
  }

  async function applyPick(andPush = false, pushRemote = "origin") {
    if (!repoPath || queue.length === 0) return;
    pendingFinalize = andPush ? { remote: pushRemote, createPr: false } : null;
    await captureSquashBase();
    if (!(await maybeAutoStash())) return;
    await applyPickShas(queue.map((c) => c.sha), andPush, pushRemote);
  }

  // ── M13 — copy + open-in-browser handlers ─────────────────
  async function openExternalUrl(url: string) {
    try {
      await openUrl(url);
    } catch (e) {
      showToast("error", `Could not open URL: ${String(e)}`);
    }
  }

  function onCopyTo(label: string) {
    showToast("success", `${label} copied`);
  }

  // ── M14 — forge connection + create PR ────────────────────
  /** Fetch open PRs for the current target branch (best-effort).
   * No connection / no remote / no project path → empty list. */
  async function refreshTargetPRs() {
    if (!forgeConnection || !repoPath || !targetBranch) {
      targetBranchPRs = [];
      return;
    }
    // Pick the first remote that resolves to a project path — usually `origin`.
    const remoteCandidates = [...remotes];
    remoteCandidates.sort((a, b) => (a.name === "origin" ? -1 : b.name === "origin" ? 1 : 0));
    let projectPath = "";
    for (const r of remoteCandidates) {
      const p = projectPathForRemote(r.name);
      if (p) { projectPath = p; break; }
    }
    if (!projectPath) {
      targetBranchPRs = [];
      return;
    }
    prCheckLoading = true;
    try {
      targetBranchPRs = await rpc.forge.listPRs({
        repoPath,
        projectPath,
        head: targetBranch,
      });
    } catch {
      // Best-effort — silently leave list empty on API errors
      targetBranchPRs = [];
    } finally {
      prCheckLoading = false;
    }
  }

  /** Refresh forgeConnection from settings (after Save in ConnectForge). */
  async function refreshForgeConnection() {
    try {
      const s = await rpc.settings.load();
      settings = s;
      forgeConnection = repoPath ? (s.forgeConnections?.[repoPath] ?? null) : null;
    } catch {
      /* ignore */
    }
  }

  /** M14f — open the Connect dialog for an arbitrary repo (may not be the open one). */
  async function openConnectForge(path: string) {
    if (!path) return;
    let rem: Remote[] = [];
    if (path === repoPath) {
      rem = remotes;
    } else {
      try { rem = await rpc.git.remotes(path); } catch { rem = []; }
    }
    connectForgeRepo = path;
    connectForgeRemotes = rem;
    // Keep Settings open underneath — ConnectForge overlays on top of it and
    // closing it returns to Settings (M14f UX fix).
    connectForgeOpen = true;
  }

  /** M14f — disconnect a forge connection for an arbitrary repo. */
  async function disconnectForge(path: string) {
    try {
      await rpc.forge.deleteConnection(path);
      await refreshForgeConnection();
      showToast("success", "Forge connection removed");
    } catch (e) {
      showToast("error", `Could not disconnect: ${String(e)}`);
    }
  }

  /** Pick the default PR base. Prefers the repo's detected default branch (from
   * `origin/HEAD` or "main"/"master"/"develop" convention). Falls back to the
   * first local branch that isn't the head. Never returns the head branch
   * itself (PR head == base is invalid). */
  function pickDefaultBase(): string {
    const head = targetBranch;
    if (defaultBranch && defaultBranch !== head) return defaultBranch;
    // Try common names that aren't the head
    for (const candidate of ["main", "master", "develop"]) {
      if (candidate !== head && branches.some((b) => !b.remote && b.name === candidate)) {
        return candidate;
      }
    }
    // Fall back to any non-head local branch
    const fallback = branches.find((b) => !b.remote && b.name !== head);
    return fallback?.name ?? "";
  }

  /** Derive owner/repo (GitHub) or namespace/path (GitLab) from the remote URL.
   * Returns empty string if the URL can't be parsed. */
  function projectPathForRemote(remoteName: string): string {
    const r = remotes.find((x) => x.name === remoteName);
    if (!r) return "";
    const info = parseForge(r.pushUrl || r.fetchUrl);
    return info?.path ?? "";
  }

  /** Open the CreatePR dialog directly without any cherry-pick. Use case:
   * user already has commits on the branch (pushed via terminal / another tool)
   * and just wants to file a PR. Picks the first remote that resolves to a
   * project path — usually `origin`. Auto-fills the title/body from the most
   * recent commit on the target branch so the user has a sensible default. */
  async function openCreatePrDirect() {
    if (!forgeConnection) {
      showToast("error", "Connect this repo to a forge first (Settings → Connected accounts).");
      return;
    }
    if (!targetBranch) {
      showToast("error", "Pick a target branch first.");
      return;
    }
    // Find a remote whose URL parses to a project path
    const candidates = [...remotes].sort((a, b) =>
      a.name === "origin" ? -1 : b.name === "origin" ? 1 : 0
    );
    let chosenRemote = "";
    for (const r of candidates) {
      const p = projectPathForRemote(r.name);
      if (p) { chosenRemote = r.name; break; }
    }
    if (!chosenRemote) {
      showToast("error", "No remote URL maps to a forge project — check the remote points to GitHub/GitLab/Bitbucket.");
      return;
    }
    lastPushRemote = chosenRemote;
    // Best-effort: fetch the most recent commit on the target branch to seed
    // the title/body. Empty list on any error — user can still type manually.
    try {
      lastAppliedCommits = await rpc.git.commits(repoPath, targetBranch, 1, 0, {});
    } catch {
      lastAppliedCommits = [];
    }
    createPrOpen = true;
  }

  /** Launched from PickQueue: "Apply & Push & Create PR" mode. */
  async function applyPushCreatePR(remote: string) {
    if (!repoPath || queue.length === 0) return;
    if (!forgeConnection) {
      showToast("error", "Connect this repo to a forge first (Settings → Connected accounts).");
      return;
    }
    // Snapshot the queue BEFORE apply — applyPickShas clears selectionMap.
    const snapshot = queue.slice();
    lastAppliedCommits = snapshot;
    lastPushRemote = remote;
    // pendingFinalize survives across conflict resolution so the PR dialog
    // opens even if cherry-pick stops mid-way for a conflict.
    pendingFinalize = { remote, createPr: true };
    await captureSquashBase();
    if (!(await maybeAutoStash())) return;
    await applyPickShas(snapshot.map((c) => c.sha), true, remote);
  }

  async function handleCreatePR(args: { base: string; head: string; title: string; body: string; draft: boolean }) {
    if (!forgeConnection) throw new Error("No forge connection");
    const projectPath = projectPathForRemote(lastPushRemote);
    if (!projectPath) throw new Error(`Could not derive project path from remote "${lastPushRemote}"`);
    const result = await rpc.forge.createPR({
      repoPath,
      projectPath,
      base: args.base,
      head: args.head,
      title: args.title,
      body: args.body,
      draft: args.draft,
    });
    createPrOpen = false;
    const label = forgeConnection.kind === "gitlab" ? "MR" : "PR";
    if (result.alreadyExisted) {
      // The server returned an existing PR/MR — surface it as a friendly toast
      // with the URL so the user can jump straight to it.
      showToast("warning", `${label} #${result.number} already exists for this branch`, result.url);
    } else {
      // Don't auto-open the browser — show toast with clickable URL.
      showToast("success", `${label} #${result.number} created`, result.url);
    }
    // The PR list for this branch just changed — refresh so the PickQueue badge
    // reflects the new state without needing to re-pick the branch.
    refreshTargetPRs();
  }

  function dismissResult() {
    applyResult = null;
    applyError = "";
    conflictFiles = [];
    conflictSha = "";
    resolvedSet = new Set();
  }

  // ── global keyboard shortcuts ─────────────────────────────
  function handleGlobalKey(e: KeyboardEvent) {
    const target = e.target as HTMLElement;
    if (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable) return;
    if (e.ctrlKey && e.key === "Enter" && !e.shiftKey && queue.length > 0 && !busy && repoPath) {
      e.preventDefault();
      applyPick(false);
    }
    if (e.ctrlKey && !e.shiftKey && e.key === "z") {
      e.preventDefault();
      undo();
    }
  }
</script>

<svelte:window onkeydown={handleGlobalKey} />

<div class="app">
  <Toolbar {repoPath} {currentBranch} {recentRepos} {consoleOpen} onopen={openRepo} onsettings={() => (settingsOpen = true)} onconsole={() => (consoleOpen = !consoleOpen)} />
  {#if pendingUpdate}
    <UpdateBanner
      version={pendingUpdate.version}
      downloading={updateDownloading}
      progress={updateProgress}
      onupdate={installUpdate}
      ondismiss={() => (pendingUpdate = null)}
    />
  {/if}

  {#if repoPath}
    <div class="workspace">
      <div class="left-pane">
        <CommitList
          {branches}
          {sourceBranch}
          {commits}
          selected={new Set(selectionMap.keys())}
          selectedSha={selectedCommit?.sha ?? ""}
          loading={loadingCommits}
          {refreshing}
          applied={appliedShas}
          onsourcebranch={changeSourceBranch}
          ontoggle={toggleCommit}
          onselect={selectCommit}
          onfetch={doFetch}
          onpull={doPull}
          onfilter={applyCommitFilter}
        />
      </div>
      <div class="col-resize-handle" onmousedown={startColResize} role="separator" aria-label="Resize panels"></div>
      <div class="right-pane" style="width: {pickQueueWidth}px">
        <PickQueue
          {queue}
          {branches}
          {remotes}
          {targetBranch}
          {sourceBranch}
          {busy}
          {progress}
          {dryRunMap}
          defaultApplyMode={settings.defaultApplyMode}
          {forgeConnected}
          targetPRs={targetBranchPRs}
          prCheckLoading={prCheckLoading}
          onopenpr={openExternalUrl}
          onrefreshprs={refreshTargetPRs}
          oncreatepr={openCreatePrDirect}
          ontargetbranch={(b) => { targetBranch = b; applyResult = null; applyError = ""; scheduleDryRun(); refreshApplied(); }}
          onremove={removeFromQueue}
          onreorder={reorderQueue}
          onapply={() => applyPick(false)}
          onapplypush={(remote) => applyPick(true, remote)}
          onapplypushpr={(remote) => applyPushCreatePR(remote)}
          oncancel={cancelPick}
          oncreate={createBranch}
          squashMode={squashActive}
          {squashMessage}
          {messageOverrides}
          onsquashtoggle={(on) => { squashMode = on; if (on && !squashMessage.trim()) squashMessage = defaultSquashMessage(); }}
          onsquashmessage={(m) => (squashMessage = m)}
          oneditmessage={(sha, msg) => {
            const m = new Map(messageOverrides);
            if (msg) m.set(sha, msg); else m.delete(sha);
            messageOverrides = m;
          }}
        />
      </div>
    </div>

    {#if selectedCommit}
      <div class="detail-resize-handle" onmousedown={startDetailResize} role="separator" aria-label="Resize detail panel"></div>
      <div class="detail-area" style="height: {detailHeight}px; grid-template-columns: 1fr {pickQueueWidth + 5}px">
        {#if detailError}
          <div class="detail-error">{detailError}</div>
        {:else}
          <CommitDetailPanel
            detail={commitDetail}
            loading={loadingDetail}
            {remotes}
            oncopy={onCopyTo}
            onopenurl={openExternalUrl}
          />
          <CommitFilesPanel
            files={commitFiles}
            loading={loadingDetail}
            selectedPath={selectedFile?.path ?? ""}
            onselect={selectFile}
            target={targetBranch}
            picking={partialPicking}
            onpartialpick={partialPick}
          />
        {/if}
      </div>
    {/if}

    {#if conflictFiles.length > 0}
      <ConflictResolver
        files={conflictFiles}
        conflictSha={conflictSha}
        queue={queue}
        dryRunMap={dryRunMap}
        remainingCommitFiles={remainingCommitFiles}
        busy={conflictBusy}
        resolvedSet={resolvedSet}
        onresolve={resolveConflictFile}
        oncontinue={continueCherry}
        onabort={abortConflict}
        onviewfile={viewConflictFile}
        onviewdiff={viewConflictFileDiff}
        aiEnabled={aiAvailable}
        aiBusy={aiBusy}
        aiResolvedSet={new Set(aiResolved.keys())}
        onairesolveall={aiResolveAll}
        onaireview={aiReview}
        onaiaccept={aiAccept}
        onaidiscard={aiDiscard}
      />
    {/if}

    {#if aiReviewOpen}
      <AiReviewModal
        file={aiReviewFile}
        diffResult={aiReviewDiff}
        originalText={aiBackup.get(aiReviewFile) ?? ""}
        resolvedText={aiResolved.get(aiReviewFile) ?? ""}
        showEol={settings.showEolMarkers}
        busy={conflictBusy}
        onaccept={() => aiAccept(aiReviewFile)}
        ondiscard={() => aiDiscard(aiReviewFile)}
        onclose={() => (aiReviewOpen = false)}
      />
    {/if}

    <ResultBanner
      result={applyResult}
      error={applyError}
      {targetBranch}
      ondismiss={dismissResult}
    />
  {:else}
    <div class="welcome">
      <p>Open a Git repository to get started.</p>
    </div>
  {/if}

  {#if consoleOpen}
    <div class="console-resize-handle" onmousedown={startConsoleResize} role="separator" aria-label="Resize console"></div>
    <GitConsole height={consoleHeight} onclose={() => (consoleOpen = false)} />
  {/if}

  {#if settingsOpen}
    <SettingsModal
      {settings}
      forgeConnection={forgeConnection}
      forgeConnectAvailable={!!repoPath}
      currentRepo={repoPath}
      recents={recentRepos.map((r) => r.path)}
      onclose={() => (settingsOpen = false)}
      onsave={saveSettings}
      onchecknow={checkForUpdates}
      onconnectforge={openConnectForge}
      ondisconnectforge={disconnectForge}
      ontestforge={(path) => rpc.forge.testSavedConnection(path)}
    />
  {/if}

  {#if connectForgeOpen && connectForgeRepo}
    <ConnectForge
      repoPath={connectForgeRepo}
      remotes={connectForgeRemotes}
      onclose={() => (connectForgeOpen = false)}
      onsaved={async () => {
        await refreshForgeConnection();
        showToast("success", "Forge connection saved");
      }}
    />
  {/if}

  {#if createPrOpen && forgeConnection}
    <CreatePR
      forgeKind={forgeConnection.kind}
      projectPath={projectPathForRemote(lastPushRemote)}
      headBranch={targetBranch}
      defaultBase={pickDefaultBase()}
      {branches}
      appliedCommits={lastAppliedCommits}
      onclose={() => (createPrOpen = false)}
      onsubmit={handleCreatePR}
    />
  {/if}
</div>

<Toast {toasts} ondismiss={dismissToast} onurlclick={openExternalUrl} />

<style>
  :global(*) { box-sizing: border-box; margin: 0; padding: 0; }
  :global(body) {
    background: #1e1e1e;
    color: #f0f0f0;
    font-family: Inter, Avenir, Helvetica, Arial, sans-serif;
    font-size: 14px;
    --border: #3a3a3a;
    --border-subtle: #2e2e2e;
    --toolbar-bg: #252525;
    --input-bg: #2a2a2a;
    --hover: #333333;
    --selected: #1a2a4a;
    --accent: #4a7ef5;
    --text: #f0f0f0;
    --text-secondary: #ccc;
    --text-muted: #888;
    --surface: #252525;
    --surface-elevated: #2c2c2c;
  }
  :global(body.light) {
    background: #f5f5f5;
    color: #1a1a1a;
    --border: #d0d0d0;
    --border-subtle: #e4e4e4;
    --toolbar-bg: #ffffff;
    --input-bg: #eeeeee;
    --hover: #e4e4e4;
    --selected: #dce8ff;
    --accent: #2563eb;
    --text: #1a1a1a;
    --text-secondary: #444;
    --text-muted: #888;
    --surface: #ffffff;
    --surface-elevated: #f8f8f8;
  }
  .app {
    display: flex;
    flex-direction: column;
    height: 100vh;
    overflow: hidden;
  }
  .workspace {
    display: flex;
    flex: 1;
    overflow: hidden;
    min-height: 0;
  }
  .left-pane {
    flex: 1;
    min-width: 0;
    overflow: hidden;
  }
  .right-pane {
    flex-shrink: 0;
    overflow: hidden;
  }
  .col-resize-handle {
    flex-shrink: 0;
    width: 5px;
    background: var(--border, #3a3a3a);
    cursor: ew-resize;
    transition: background 0.15s;
  }
  .col-resize-handle:hover { background: var(--accent, #4a7ef5); }
  .detail-resize-handle {
    flex-shrink: 0;
    height: 5px;
    background: var(--border, #3a3a3a);
    cursor: ns-resize;
    transition: background 0.15s;
  }
  .detail-resize-handle:hover { background: var(--accent, #4a7ef5); }
  .console-resize-handle {
    flex-shrink: 0;
    height: 5px;
    background: #2a2a2a;
    cursor: ns-resize;
    transition: background 0.15s;
  }
  .console-resize-handle:hover { background: #4a7ef5; }
  .detail-area {
    display: grid;
    grid-template-columns: 1fr 340px;
    flex-shrink: 0;
    border-top: none;
    overflow: hidden;
  }
  .detail-error {
    grid-column: 1 / -1;
    padding: 0.75rem 1rem;
    font-size: 0.82rem;
    color: #ef5350;
    font-family: ui-monospace, monospace;
  }
  .welcome {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--text-muted);
    font-size: 0.95rem;
  }
</style>
