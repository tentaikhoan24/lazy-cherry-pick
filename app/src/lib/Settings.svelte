<script lang="ts">
  import { invoke } from "@tauri-apps/api/core";
  import { getVersion } from "@tauri-apps/api/app";
  import { open as openDialog } from "@tauri-apps/plugin-dialog";
  import type { AppSettings, DetectedTool, DetectedAi, ForgeConnection, ForgeConnectionTest } from "./rpc-types";
  import { AI_PROVIDERS, findProvider } from "./ai-providers";

  interface Props {
    settings: AppSettings;
    onclose: () => void;
    onsave: (s: AppSettings) => void;
    onchecknow?: () => Promise<boolean>;
    /** M14 — current repo's forge connection, null if not configured. */
    forgeConnection?: ForgeConnection | null;
    /** True when a repo is open — controls availability of Connect/Disconnect. */
    forgeConnectAvailable?: boolean;
    /** M14f — path of the currently-open repo ("" if none), to badge "current". */
    currentRepo?: string;
    /** M14f — recently-opened repo paths, for the "connect another repo" dropdown. */
    recents?: string[];
    /** Called with the repo path to connect/disconnect/test. */
    onconnectforge?: (repoPath: string) => void;
    ondisconnectforge?: (repoPath: string) => void;
    /** M14f — test a saved connection; resolves on success, rejects on auth failure. */
    ontestforge?: (repoPath: string) => Promise<ForgeConnectionTest>;
  }

  let { settings, onclose, onsave, onchecknow, forgeConnection = null, forgeConnectAvailable = false, currentRepo = "", recents = [], onconnectforge, ondisconnectforge, ontestforge }: Props = $props();

  // ── M14f — Accounts management state ──────────────────────
  /** Per-repo test status shown inline in the Accounts list. */
  let forgeTest = $state<Record<string, { status: "testing" | "ok" | "err"; msg?: string }>>({});
  /** Selected repo path in the "connect another repo" dropdown. */
  let connectPick = $state("");

  const connectedRepos = $derived(Object.keys(settings.forgeConnections ?? {}).sort());
  // Recents that aren't already connected — candidates for a new connection.
  const connectableRecents = $derived(recents.filter((p) => !(settings.forgeConnections ?? {})[p]));

  function repoName(path: string): string {
    return path.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || path;
  }
  function forgeLabel(kind: string): string {
    return kind === "github" ? "GitHub" : kind === "gitlab" ? "GitLab" : kind === "bitbucket" ? "Bitbucket" : kind;
  }

  async function testForge(path: string) {
    if (!ontestforge) return;
    forgeTest = { ...forgeTest, [path]: { status: "testing" } };
    try {
      const r = await ontestforge(path);
      forgeTest = { ...forgeTest, [path]: { status: "ok", msg: `${r.username}${r.scopes.length ? " · " + r.scopes.join(", ") : ""}` } };
    } catch (e) {
      forgeTest = { ...forgeTest, [path]: { status: "err", msg: String(e) } };
    }
  }

  let activeTab = $state<"general" | "ai" | "tools" | "accounts">("general");

  // ── modal resize (drag bottom-right corner) ───────────────
  let modalWidth = $state(660);
  let modalBodyHeight = $state(460);
  // Suppresses the overlay's onclose when a resize-drag ends with the
  // cursor outside the (now-shrunk) modal — see startModalResize.
  let resizingModal = false;

  function startModalResize(e: MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    resizingModal = true;
    const startX = e.clientX;
    const startY = e.clientY;
    const startW = modalWidth;
    const startH = modalBodyHeight;
    function onMove(ev: MouseEvent) {
      modalWidth = Math.max(560, Math.min(window.innerWidth - 32, startW + (ev.clientX - startX)));
      modalBodyHeight = Math.max(320, Math.min(window.innerHeight - 160, startH + (ev.clientY - startY)));
    }
    function onUp() {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
      // Defer reset until after the synthetic "click" the browser fires on
      // mouseup (its target may be the overlay if the modal shrank away
      // from the cursor) has been dispatched and ignored below.
      setTimeout(() => { resizingModal = false; }, 0);
    }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }

  let maxCommits = $state(settings.maxCommits);
  let defaultApplyMode = $state(settings.defaultApplyMode);
  let showEolMarkers = $state(settings.showEolMarkers);
  let autoFetchOnOpen = $state(settings.autoFetchOnOpen);
  let autoStash = $state(settings.autoStash);
  let checkForUpdatesOnStartup = $state(settings.checkForUpdatesOnStartup);
  let theme = $state(settings.theme);
  let checkingNow = $state(false);
  let checkResult = $state<"up-to-date" | "found" | null>(null);
  let externalDiffEnabled = $state(settings.externalDiffEnabled);
  let externalDiffPath = $state(settings.externalDiffPath);
  let externalDiffArgs = $state(settings.externalDiffArgs);
  let externalMergeEnabled = $state(settings.externalMergeEnabled);
  let externalMergePath = $state(settings.externalMergePath);
  let externalMergeArgs = $state(settings.externalMergeArgs);
  // M16/M16b — AI conflict resolution (generic engine + provider presets)
  let aiEnabled = $state(settings.aiEnabled);
  let aiProvider = $state(settings.aiProvider || "claude");
  let aiCommand = $state(settings.aiCommand);
  let aiArgs = $state(settings.aiArgs);
  let aiModel = $state(settings.aiModel);
  let aiPromptVia = $state(settings.aiPromptVia || "stdin");
  let aiOutputFormat = $state(settings.aiOutputFormat || "claude-json");
  let aiTimeoutSecs = $state(settings.aiTimeoutSecs);
  let detectingAi = $state(false);
  let aiDetectResult = $state<DetectedAi | null>(null);
  let showAiAdvanced = $state(false);
  const currentProvider = $derived(findProvider(aiProvider));

  /** Apply a preset's defaults into the editable fields (command stays unless empty). */
  function applyProvider(id: string) {
    aiProvider = id;
    aiDetectResult = null;
    const p = findProvider(id);
    if (id !== "custom") {
      aiCommand = p.command;
      aiArgs = p.args;
      aiPromptVia = p.promptVia;
      aiOutputFormat = p.outputFormat;
      // Reset model if the new provider doesn't offer the current value.
      if (p.models.length && !p.models.some((m) => m.value === aiModel)) aiModel = "";
    }
  }

  // App version — pulled from tauri.conf.json at runtime via Tauri API.
  // Async (waits for IPC). Empty string until resolved, so the badge area
  // can render its layout immediately and fill in the version when ready.
  let appVersion = $state("");
  getVersion().then((v) => { appVersion = v; }).catch(() => {});

  let detectedTools = $state<DetectedTool[]>([]);
  let detecting = $state(false);
  let showRef = $state(false);

  const DIFF_ARGS_HINT = 'Placeholders: {left} {right} {leftLabel} {rightLabel}';
  const MERGE_ARGS_HINT = 'Placeholders: {base} {ours} {theirs} {output}';
  const DIFF_ARGS_PLACEHOLDER = '/command:diff /path:"{left}" /path2:"{right}"';
  const MERGE_ARGS_PLACEHOLDER = '/command:merge /path:"{output}" /base:"{base}" /theirs:"{theirs}" /mine:"{ours}"';

  const TOOL_DIFF_ARGS: Record<string, string> = {
    // TortoiseGit: /path2: appears on LEFT pane, /path: appears on RIGHT pane
    'TortoiseGit': '/command:diff /path2:"{left}" /path:"{right}"',
    'Beyond Compare 3': '"{left}" "{right}"',
    'Beyond Compare 4': '"{left}" "{right}"',
    'WinMerge': '"{left}" "{right}"',
    'VSCode': '--diff "{left}" "{right}"',
  };
  const TOOL_MERGE_ARGS: Record<string, string> = {
    // TortoiseGitMerge.exe (separate exe from TortoiseGitProc.exe) for 3-way conflict resolution
    'TortoiseGit': '/base:"{base}" /theirs:"{theirs}" /mine:"{ours}" /merged:"{output}"',
    'Beyond Compare 3': '"{theirs}" "{ours}" "{base}" "{output}"',
    'Beyond Compare 4': '"{theirs}" "{ours}" "{base}" "{output}"',
    'WinMerge': '/e /ub /wl /wr "{ours}" "{base}" "{theirs}" "{output}"',
  };

  function save() {
    onsave({
      maxCommits, defaultApplyMode, showEolMarkers, autoFetchOnOpen, autoStash, theme,
      externalDiffEnabled, externalDiffPath, externalDiffArgs,
      externalMergeEnabled, externalMergePath, externalMergeArgs,
      checkForUpdatesOnStartup,
      aiEnabled, aiProvider, aiCommand, aiArgs, aiModel, aiPromptVia, aiOutputFormat, aiTimeoutSecs,
      forgeConnections: settings.forgeConnections,
    });
    onclose();
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") onclose();
  }

  async function autoDetect() {
    detecting = true;
    try {
      detectedTools = await invoke<DetectedTool[]>("detect_external_tools");
    } catch {
      detectedTools = [];
    } finally {
      detecting = false;
    }
  }

  function applyDetected(t: DetectedTool) {
    externalDiffPath = t.path;
    // TortoiseGit uses a separate exe (TortoiseGitMerge.exe) for 3-way conflict resolution,
    // located in the same directory as TortoiseGitProc.exe.
    externalMergePath = t.name === 'TortoiseGit'
      ? t.path.replace('TortoiseGitProc.exe', 'TortoiseGitMerge.exe')
      : t.path;
    if (TOOL_DIFF_ARGS[t.name]) externalDiffArgs = TOOL_DIFF_ARGS[t.name];
    if (TOOL_MERGE_ARGS[t.name]) externalMergeArgs = TOOL_MERGE_ARGS[t.name];
  }

  async function browseDiffExe() {
    const result = await openDialog({
      title: "Select diff tool executable",
      filters: [{ name: "Executable", extensions: ["exe"] }],
      multiple: false,
    });
    if (result) externalDiffPath = result as string;
  }

  async function browseMergeExe() {
    const result = await openDialog({
      title: "Select merge tool executable",
      filters: [{ name: "Executable", extensions: ["exe"] }],
      multiple: false,
    });
    if (result) externalMergePath = result as string;
  }

  async function detectAi() {
    detectingAi = true;
    try {
      const name = aiCommand || currentProvider.command;
      const r = await invoke<DetectedAi>("detect_ai_tool", { command: name });
      aiDetectResult = r;
      if (r.found) aiCommand = r.path;
    } catch {
      aiDetectResult = { found: false, path: "", version: "" };
    } finally {
      detectingAi = false;
    }
  }

  async function browseAiExe() {
    const result = await openDialog({
      title: "Select AI CLI executable",
      filters: [{ name: "Executable", extensions: ["cmd", "exe", "bat"] }],
      multiple: false,
    });
    if (result) aiCommand = result as string;
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div
  class="overlay"
  onclick={() => {
    if (resizingModal) { resizingModal = false; return; }
    onclose();
  }}
  role="presentation"
>
  <div class="modal" style="width: {modalWidth}px" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true" aria-label="Settings">
    <div class="modal-header">
      <span class="modal-title">Settings</span>
      <button class="close-btn" onclick={onclose} aria-label="Close">✕</button>
    </div>

    <div class="modal-body" style="height: {modalBodyHeight}px">
      <div class="settings-sidebar">
        <button class="tab-btn" class:active={activeTab === "general"} onclick={() => (activeTab = "general")}>
          <span class="tab-icon">⚙</span> General
        </button>
        <button class="tab-btn" class:active={activeTab === "ai"} onclick={() => (activeTab = "ai")}>
          <span class="tab-icon">🤖</span> AI Resolution
        </button>
        <button class="tab-btn" class:active={activeTab === "tools"} onclick={() => (activeTab = "tools")}>
          <span class="tab-icon">🔧</span> External Tools
        </button>
        <button class="tab-btn" class:active={activeTab === "accounts"} onclick={() => (activeTab = "accounts")}>
          <span class="tab-icon">🔗</span> Accounts
        </button>
      </div>

      <div class="settings-content">
        {#if activeTab === "general"}
          <h3 class="tab-heading">General</h3>

          <div class="setting-row">
            <label class="setting-label" for="max-commits">Max commits to load</label>
            <input
              id="max-commits"
              type="number"
              class="setting-input"
              bind:value={maxCommits}
              min={10}
              max={5000}
              step={50}
            />
          </div>

          <div class="setting-row">
            <label class="setting-label" for="apply-mode">Default apply mode</label>
            <select id="apply-mode" class="setting-select" bind:value={defaultApplyMode}>
              <option value="apply">Apply only</option>
              <option value="apply-push">Apply &amp; Push</option>
            </select>
          </div>

          <div class="setting-row">
            <span class="setting-label">Show EOL markers (¶)</span>
            <button
              class="toggle"
              class:on={showEolMarkers}
              onclick={() => (showEolMarkers = !showEolMarkers)}
              aria-checked={showEolMarkers}
              role="switch"
            >
              {showEolMarkers ? "On" : "Off"}
            </button>
          </div>

          <div class="setting-row">
            <span class="setting-label">Auto-fetch on repo open</span>
            <button
              class="toggle"
              class:on={autoFetchOnOpen}
              onclick={() => (autoFetchOnOpen = !autoFetchOnOpen)}
              aria-checked={autoFetchOnOpen}
              role="switch"
            >
              {autoFetchOnOpen ? "On" : "Off"}
            </button>
          </div>

          <div class="setting-row">
            <span class="setting-label">
              Auto-stash before apply
              <span class="setting-hint">Stash uncommitted changes before a cherry-pick batch and restore them after</span>
            </span>
            <button
              class="toggle"
              class:on={autoStash}
              onclick={() => (autoStash = !autoStash)}
              aria-checked={autoStash}
              role="switch"
            >
              {autoStash ? "On" : "Off"}
            </button>
          </div>

          <div class="setting-row">
            <span class="setting-label">Theme</span>
            <div class="theme-seg">
              <button class="seg-btn" class:active={theme === "dark"} onclick={() => (theme = "dark")}>Dark</button>
              <button class="seg-btn" class:active={theme === "light"} onclick={() => (theme = "light")}>Light</button>
            </div>
          </div>

          <div class="setting-row">
            <span class="setting-label">Check for updates on startup</span>
            <div class="row-right">
              <button
                class="toggle"
                class:on={checkForUpdatesOnStartup}
                onclick={() => (checkForUpdatesOnStartup = !checkForUpdatesOnStartup)}
                aria-checked={checkForUpdatesOnStartup}
                role="switch"
              >
                {checkForUpdatesOnStartup ? "On" : "Off"}
              </button>
              <button
                class="btn-check-now"
                disabled={checkingNow}
                onclick={async () => {
                  checkingNow = true;
                  checkResult = null;
                  const found = await onchecknow?.();
                  checkResult = found ? "found" : "up-to-date";
                  checkingNow = false;
                }}
              >
                {checkingNow ? "Checking…" : "Check Now"}
              </button>
              {#if checkResult === "up-to-date"}
                <span class="check-ok">✓ Up to date</span>
              {:else if checkResult === "found"}
                <span class="check-found">Update found!</span>
              {/if}
            </div>
          </div>

        {:else if activeTab === "ai"}
          <h3 class="tab-heading">AI Conflict Resolution</h3>

          <div class="tool-block">
            <div class="tool-header">
              <span class="tool-name">Resolve conflicts with an AI CLI</span>
              <button
                class="toggle"
                class:on={aiEnabled}
                onclick={() => (aiEnabled = !aiEnabled)}
                aria-checked={aiEnabled}
                role="switch"
              >
                {aiEnabled ? "On" : "Off"}
              </button>
            </div>
            {#if aiEnabled}
              <p class="ai-note">
                On conflict, click “🤖 AI resolve all” — the chosen CLI agent writes a merged
                version to disk, which you review before staging. Uses the tool's own login
                (no API key stored here). {currentProvider.note}
              </p>

              <div class="tool-field">
                <label class="field-label" for="ai-provider">Provider</label>
                <select
                  id="ai-provider"
                  class="setting-select"
                  value={aiProvider}
                  onchange={(e) => applyProvider((e.currentTarget as HTMLSelectElement).value)}
                >
                  {#each AI_PROVIDERS as p}
                    <option value={p.id}>{p.label}</option>
                  {/each}
                </select>
              </div>

              <div class="tool-field">
                <label class="field-label" for="ai-path">Executable (name or full path)</label>
                <div class="path-row">
                  <input
                    id="ai-path"
                    class="setting-input-full"
                    bind:value={aiCommand}
                    placeholder={currentProvider.command || "e.g. claude / gemini / full path"}
                    spellcheck="false"
                  />
                  <button class="browse-btn" onclick={browseAiExe} title="Browse for executable">…</button>
                </div>
                <div class="detect-row">
                  <button class="detect-btn" onclick={detectAi} disabled={detectingAi}>
                    {detectingAi ? "Detecting…" : "Detect"}
                  </button>
                  {#if aiDetectResult}
                    {#if aiDetectResult.found}
                      <span class="detect-hint ok">✓ {aiDetectResult.version || "found"}</span>
                    {:else}
                      <span class="detect-hint err">Not found — install it or set the path manually</span>
                    {/if}
                  {/if}
                </div>
              </div>

              <div class="tool-field ai-row">
                <div class="ai-field">
                  <label class="field-label" for="ai-model">Model</label>
                  {#if currentProvider.models.length}
                    <select id="ai-model" class="setting-select" bind:value={aiModel}>
                      {#each currentProvider.models as m}
                        <option value={m.value}>{m.label}</option>
                      {/each}
                    </select>
                  {:else}
                    <input
                      id="ai-model"
                      class="setting-input"
                      bind:value={aiModel}
                      placeholder="(tool default)"
                      spellcheck="false"
                    />
                  {/if}
                </div>
                <div class="ai-field">
                  <label class="field-label" for="ai-timeout">Timeout (seconds)</label>
                  <input
                    id="ai-timeout"
                    class="setting-input"
                    type="number"
                    min="10"
                    max="900"
                    bind:value={aiTimeoutSecs}
                  />
                </div>
              </div>

              <button class="advanced-toggle" onclick={() => (showAiAdvanced = !showAiAdvanced)}>
                {showAiAdvanced ? "▾" : "▸"} Advanced (command flags)
              </button>
              {#if showAiAdvanced}
                <div class="tool-field">
                  <label class="field-label" for="ai-args">Args template</label>
                  <input
                    id="ai-args"
                    class="setting-input-full"
                    bind:value={aiArgs}
                    placeholder="flags… use {'{model}'} and {'{prompt}'}"
                    spellcheck="false"
                  />
                  <span class="ai-note">
                    <code>{'{model}'}</code> → model value (dropped with its flag when empty).
                    <code>{'{prompt}'}</code> → the prompt, only when “Prompt via” is <em>arg</em>.
                  </span>
                </div>
                <div class="tool-field ai-row">
                  <div class="ai-field">
                    <label class="field-label" for="ai-prompt-via">Prompt via</label>
                    <select id="ai-prompt-via" class="setting-select" bind:value={aiPromptVia}>
                      <option value="stdin">stdin (safest)</option>
                      <option value="arg">argument ({'{prompt}'})</option>
                    </select>
                  </div>
                  <div class="ai-field">
                    <label class="field-label" for="ai-output">Output format</label>
                    <select id="ai-output" class="setting-select" bind:value={aiOutputFormat}>
                      <option value="claude-json">claude-json (cost + status)</option>
                      <option value="none">none (exit code only)</option>
                    </select>
                  </div>
                </div>
              {/if}
            {/if}
          </div>

        {:else if activeTab === "tools"}
          <h3 class="tab-heading">External Tools</h3>

          <!-- Auto-detect -->
          <div class="detect-row">
            <button class="detect-btn" onclick={autoDetect} disabled={detecting}>
              {detecting ? "Detecting…" : "Auto-detect installed tools"}
            </button>
            {#if detectedTools.length > 0}
              <div class="detected-pills">
                {#each detectedTools as t}
                  <button class="pill" onclick={() => applyDetected(t)} title="Fill path with {t.name}">{t.name}</button>
                {/each}
              </div>
            {:else if !detecting}
              <span class="detect-hint">Click to scan common install paths</span>
            {/if}
          </div>

          <!-- External Diff Viewer -->
          <div class="tool-block">
            <div class="tool-header">
              <span class="tool-name">External Diff Viewer</span>
              <button
                class="toggle"
                class:on={externalDiffEnabled}
                onclick={() => (externalDiffEnabled = !externalDiffEnabled)}
                aria-checked={externalDiffEnabled}
                role="switch"
              >
                {externalDiffEnabled ? "On" : "Off"}
              </button>
            </div>
            {#if externalDiffEnabled}
              <div class="tool-field">
                <label class="field-label" for="diff-path">Executable path</label>
                <div class="path-row">
                  <input
                    id="diff-path"
                    class="setting-input-full"
                    bind:value={externalDiffPath}
                    placeholder='C:\Program Files\TortoiseGit\bin\TortoiseGitProc.exe'
                    spellcheck="false"
                  />
                  <button class="browse-btn" onclick={browseDiffExe} title="Browse for executable">…</button>
                </div>
              </div>
              <div class="tool-field">
                <label class="field-label" for="diff-args">Arguments template</label>
                <input
                  id="diff-args"
                  class="setting-input-full"
                  bind:value={externalDiffArgs}
                  placeholder={DIFF_ARGS_PLACEHOLDER}
                  spellcheck="false"
                />
                <span class="arg-hint">{DIFF_ARGS_HINT}</span>
              </div>
            {/if}
          </div>

          <!-- External Merge Tool -->
          <div class="tool-block">
            <div class="tool-header">
              <span class="tool-name">External Merge Tool</span>
              <button
                class="toggle"
                class:on={externalMergeEnabled}
                onclick={() => (externalMergeEnabled = !externalMergeEnabled)}
                aria-checked={externalMergeEnabled}
                role="switch"
              >
                {externalMergeEnabled ? "On" : "Off"}
              </button>
            </div>
            {#if externalMergeEnabled}
              <div class="tool-field">
                <label class="field-label" for="merge-path">Executable path</label>
                <div class="path-row">
                  <input
                    id="merge-path"
                    class="setting-input-full"
                    bind:value={externalMergePath}
                    placeholder='C:\Program Files\TortoiseGit\bin\TortoiseGitProc.exe'
                    spellcheck="false"
                  />
                  <button class="browse-btn" onclick={browseMergeExe} title="Browse for executable">…</button>
                </div>
              </div>
              <div class="tool-field">
                <label class="field-label" for="merge-args">Arguments template</label>
                <input
                  id="merge-args"
                  class="setting-input-full"
                  bind:value={externalMergeArgs}
                  placeholder={MERGE_ARGS_PLACEHOLDER}
                  spellcheck="false"
                />
                <span class="arg-hint">{MERGE_ARGS_HINT}</span>
              </div>
            {/if}
          </div>

          <!-- Reference -->
          <button class="ref-toggle" onclick={() => (showRef = !showRef)}>
            {showRef ? "▾" : "▸"} Reference: common tool args
          </button>
          {#if showRef}
            <table class="ref-table">
              <thead><tr><th>Tool</th><th>Diff args</th><th>Merge args</th></tr></thead>
              <tbody>
                <tr>
                  <td>TortoiseGit<br/><small class="ref-note">diff: TortoiseGitProc.exe<br/>merge: TortoiseGitMerge.exe</small></td>
                  <td><code>/command:diff /path:"{"{left}"}" /path2:"{"{right}"}"</code></td>
                  <td><code>/base:"{"{base}"}" /theirs:"{"{theirs}"}" /mine:"{"{ours}"}" /merged:"{"{output}"}"</code></td>
                </tr>
                <tr>
                  <td>Beyond Compare</td>
                  <td><code>"{"{left}"}" "{"{right}"}"</code></td>
                  <td><code>"{"{theirs}"}" "{"{ours}"}" "{"{base}"}" "{"{output}"}"</code></td>
                </tr>
                <tr>
                  <td>WinMerge</td>
                  <td><code>"{"{left}"}" "{"{right}"}"</code></td>
                  <td><code>/e /ub /wl /wr "{"{ours}"}" "{"{base}"}" "{"{theirs}"}" "{"{output}"}"</code></td>
                </tr>
                <tr>
                  <td>VSCode</td>
                  <td><code>--diff "{"{left}"}" "{"{right}"}"</code></td>
                  <td>—</td>
                </tr>
              </tbody>
            </table>
          {/if}

        {:else if activeTab === "accounts"}
          <h3 class="tab-heading">Connected Accounts</h3>
          <p class="tab-sub">Forge connections (for PR/MR creation), one per repository. Tokens live in the OS keychain.</p>

          {#if connectedRepos.length === 0}
            <p class="hint acc-empty">
              {forgeConnectAvailable
                ? "No forge connections yet. Connect the current repo below, or pick a recent one."
                : "Open a repo to configure a forge connection — or pick a recent repo below."}
            </p>
          {:else}
            <div class="acc-list">
              {#each connectedRepos as path (path)}
                {@const conn = (settings.forgeConnections ?? {})[path]}
                {@const t = forgeTest[path]}
                <div class="acc-card">
                  <div class="acc-card-main">
                    <div class="acc-repo">
                      <span class="acc-repo-name" title={path}>{repoName(path)}</span>
                      {#if path === currentRepo}<span class="acc-badge">current</span>{/if}
                    </div>
                    <div class="acc-conn">
                      <strong>{forgeLabel(conn.kind)}</strong> · {conn.host} · <span class="mono">{conn.username}</span>
                    </div>
                    {#if t}
                      <div class="acc-test-result" class:ok={t.status === "ok"} class:err={t.status === "err"}>
                        {#if t.status === "testing"}⏳ Testing…
                        {:else if t.status === "ok"}✓ {t.msg}
                        {:else}✗ {t.msg}{/if}
                      </div>
                    {/if}
                  </div>
                  <div class="acc-actions">
                    <button class="forge-action" onclick={() => testForge(path)} disabled={t?.status === "testing"} title="Test the saved token">Test</button>
                    <button class="forge-action" onclick={() => onconnectforge?.(path)} title="Re-enter token / change account">Reconnect</button>
                    <button class="forge-action danger" onclick={() => ondisconnectforge?.(path)} title="Remove this connection">Disconnect</button>
                  </div>
                </div>
              {/each}
            </div>
          {/if}

          <div class="acc-connect-new">
            <label class="label">Connect a repo</label>
            <div class="acc-connect-row">
              <select class="acc-select" bind:value={connectPick}>
                <option value="">{connectableRecents.length ? "Select a recent repo…" : "No unconnected recent repos"}</option>
                {#if currentRepo && !settings.forgeConnections?.[currentRepo]}
                  <option value={currentRepo}>{repoName(currentRepo)} (current)</option>
                {/if}
                {#each connectableRecents as p (p)}
                  {#if p !== currentRepo}<option value={p}>{repoName(p)}</option>{/if}
                {/each}
              </select>
              <button
                class="forge-action primary"
                disabled={!connectPick}
                onclick={() => { if (connectPick) { onconnectforge?.(connectPick); connectPick = ""; } }}
              >
                Connect…
              </button>
            </div>
            <span class="hint acc-connect-hint">Reconnecting/connecting opens the token dialog; the repo doesn't need to be open.</span>
          </div>
        {/if}
      </div>
    </div>

    <div class="modal-footer">
      <span class="version-badge" title="Installed version">
        Lazy Cherry Pick {appVersion ? `v${appVersion}` : ""}
      </span>
      <button class="cancel-btn" onclick={onclose}>Cancel</button>
      <button class="save-btn" onclick={save}>Save</button>
    </div>

    <div
      class="modal-resize-handle"
      onmousedown={startModalResize}
      role="separator"
      aria-label="Resize settings window"
    ></div>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 500;
  }
  .modal {
    position: relative;
    background: var(--surface, #252525);
    border: 1px solid var(--border, #3a3a3a);
    border-radius: 10px;
    max-width: calc(100vw - 2rem);
    max-height: 90vh;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
    display: flex;
    flex-direction: column;
  }
  .modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.75rem 1rem;
    border-bottom: 1px solid var(--border, #3a3a3a);
    flex-shrink: 0;
  }
  .modal-title {
    font-size: 0.9rem;
    font-weight: 600;
    color: var(--text, #f0f0f0);
  }
  .close-btn {
    background: none;
    border: none;
    color: var(--text-muted, #888);
    font-size: 0.85rem;
    cursor: pointer;
    padding: 0.2rem 0.4rem;
    border-radius: 4px;
  }
  .close-btn:hover { color: var(--text, #f0f0f0); background: var(--hover, #3a3a3a); }

  .modal-body {
    display: flex;
    flex: 0 1 auto;
    overflow: hidden;
  }

  /* ── resize handle (bottom-right corner) ── */
  .modal-resize-handle {
    position: absolute;
    right: 0;
    bottom: 0;
    width: 18px;
    height: 18px;
    cursor: nwse-resize;
    z-index: 10;
  }
  .modal-resize-handle::before {
    content: "";
    position: absolute;
    right: 4px;
    bottom: 4px;
    width: 8px;
    height: 8px;
    border-right: 2px solid var(--border, #555);
    border-bottom: 2px solid var(--border, #555);
    border-radius: 0 0 3px 0;
  }
  .modal-resize-handle:hover::before { border-color: var(--accent, #4a7ef5); }

  /* ── sidebar tabs ── */
  .settings-sidebar {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    width: 152px;
    flex-shrink: 0;
    padding: 0.75rem 0.5rem;
    border-right: 1px solid var(--border, #3a3a3a);
    overflow-y: auto;
  }
  .tab-btn {
    display: flex;
    align-items: center;
    gap: 0.55rem;
    padding: 0.45rem 0.6rem;
    border: none;
    border-radius: 6px;
    background: none;
    color: var(--text-secondary, #aaa);
    font-size: 0.83rem;
    text-align: left;
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .tab-btn:hover { background: var(--hover, #3a3a3a); color: var(--text, #f0f0f0); }
  .tab-btn.active {
    background: var(--selected, rgba(74, 126, 245, 0.18));
    color: var(--text, #f0f0f0);
    font-weight: 600;
  }
  .tab-icon {
    font-size: 0.95rem;
    width: 1.2em;
    text-align: center;
    flex-shrink: 0;
  }

  /* ── content pane ── */
  .settings-content {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    padding: 0.85rem 1rem;
    overflow-y: auto;
  }
  .tab-heading {
    font-size: 0.92rem;
    font-weight: 600;
    color: var(--text, #f0f0f0);
    margin: 0;
  }

  .setting-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
  }
  .setting-label {
    font-size: 0.83rem;
    color: var(--text-secondary, #ccc);
    flex: 1;
  }
  .setting-hint {
    display: block;
    font-size: 0.72rem;
    color: var(--text-muted, #888);
    margin-top: 0.15rem;
  }
  .setting-input {
    width: 90px;
    padding: 0.28rem 0.5rem;
    border-radius: 5px;
    border: 1px solid var(--border, #555);
    background: var(--input-bg, #1e1e1e);
    color: var(--text, #f0f0f0);
    font-size: 0.83rem;
    text-align: right;
    outline: none;
  }
  .setting-input:focus { border-color: var(--accent, #4a7ef5); }
  .setting-select {
    width: 140px;
    padding: 0.28rem 0.5rem;
    border-radius: 5px;
    border: 1px solid var(--border, #555);
    background: var(--input-bg, #1e1e1e);
    color: var(--text, #f0f0f0);
    font-size: 0.83rem;
    outline: none;
    cursor: pointer;
  }
  .setting-select:focus { border-color: var(--accent, #4a7ef5); }

  .toggle {
    min-width: 54px;
    padding: 0.28rem 0.65rem;
    border-radius: 99px;
    border: 1px solid var(--border, #555);
    background: var(--input-bg, #2a2a2a);
    color: var(--text-muted, #888);
    font-size: 0.78rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s, color 0.15s;
    text-align: center;
  }
  .toggle.on {
    background: var(--accent, #4a7ef5);
    border-color: var(--accent, #4a7ef5);
    color: #fff;
  }

  .forge-info {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
  }
  .connected-label {
    flex: 1;
    font-size: 0.85rem;
    color: var(--text-secondary, #ccc);
  }
  .connected-label .mono {
    font-family: ui-monospace, monospace;
    color: var(--text, #f0f0f0);
  }
  .hint {
    flex: 1;
    font-size: 0.82rem;
    color: var(--text-muted, #888);
  }
  .forge-action {
    padding: 0.3rem 0.7rem;
    border-radius: 4px;
    border: 1px solid var(--border, #555);
    background: var(--input-bg, #2a2a2a);
    color: var(--text, #f0f0f0);
    font-size: 0.82rem;
    cursor: pointer;
  }
  .forge-action:hover { background: var(--hover, #3a3a3a); }
  .forge-action.primary {
    background: var(--accent, #4a7ef5);
    border-color: var(--accent, #4a7ef5);
    color: white;
    font-weight: 600;
  }
  .forge-action.primary:hover { filter: brightness(1.1); }
  .forge-action.danger { color: #ff8888; border-color: #6a3030; }
  .forge-action.danger:hover { background: rgba(224, 85, 85, 0.12); }
  .forge-action:disabled { opacity: 0.45; cursor: not-allowed; }

  /* ── M14f — Accounts management ── */
  .tab-sub { font-size: 0.8rem; color: var(--text-muted, #888); margin: 0 0 0.4rem; }
  .acc-empty { margin: 0.4rem 0; }
  .mono { font-family: ui-monospace, monospace; color: var(--text, #f0f0f0); }
  .acc-list { display: flex; flex-direction: column; gap: 0.5rem; margin-bottom: 0.9rem; }
  .acc-card {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.5rem 0.65rem;
    border: 1px solid var(--border, #3a3a3a);
    border-radius: 6px;
    background: var(--input-bg, #1e1e1e);
  }
  .acc-card-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 0.15rem; }
  .acc-repo { display: flex; align-items: center; gap: 0.4rem; }
  .acc-repo-name { font-size: 0.85rem; font-weight: 600; color: var(--text, #f0f0f0); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .acc-badge {
    flex-shrink: 0;
    font-size: 0.66rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--accent, #4a7ef5);
    border: 1px solid var(--accent, #4a7ef5);
    border-radius: 3px;
    padding: 0 0.3rem;
  }
  .acc-conn { font-size: 0.78rem; color: var(--text-secondary, #bbb); }
  .acc-test-result { font-size: 0.74rem; color: var(--text-muted, #888); }
  .acc-test-result.ok { color: #66bb6a; }
  .acc-test-result.err { color: #e05555; }
  .acc-actions { flex-shrink: 0; display: flex; gap: 0.35rem; align-items: center; }
  .acc-connect-new { display: flex; flex-direction: column; gap: 0.35rem; margin-top: 0.5rem; padding-top: 0.7rem; border-top: 1px solid var(--border-subtle, #2e2e2e); }
  .acc-connect-row { display: flex; gap: 0.5rem; align-items: center; }
  .acc-select {
    flex: 1;
    padding: 0.35rem 0.5rem;
    border-radius: 5px;
    border: 1px solid var(--border, #555);
    background: var(--input-bg, #1e1e1e);
    color: var(--text, #f0f0f0);
    font-size: 0.82rem;
  }
  .acc-connect-hint { font-size: 0.72rem; }

  /* ── auto-detect ── */
  .detect-row {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
  }
  .detect-btn {
    padding: 0.28rem 0.75rem;
    border-radius: 5px;
    border: 1px solid var(--border, #555);
    background: var(--input-bg, #2a2a2a);
    color: var(--text-secondary, #ccc);
    font-size: 0.78rem;
    cursor: pointer;
    flex-shrink: 0;
  }
  .detect-btn:hover:not(:disabled) { background: var(--hover, #3a3a3a); color: var(--text, #f0f0f0); }
  .detect-btn:disabled { opacity: 0.5; cursor: default; }
  .detect-hint { font-size: 0.72rem; color: var(--text-muted, #666); }
  .detect-hint.ok { color: #66bb6a; }
  .detect-hint.err { color: #e05555; }
  .ai-note {
    font-size: 0.74rem;
    color: var(--text-secondary, #aaa);
    line-height: 1.45;
    margin: 0.1rem 0 0.6rem;
  }
  .ai-row { display: flex; gap: 1rem; }
  .ai-field { flex: 1; display: flex; flex-direction: column; gap: 0.25rem; }
  .ai-field .setting-input,
  .ai-field .setting-select { width: 100%; }
  .ai-note code {
    font-family: ui-monospace, monospace;
    font-size: 0.7rem;
    background: var(--input-bg, #2a2a2a);
    padding: 0.05rem 0.3rem;
    border-radius: 3px;
  }
  .advanced-toggle {
    background: none;
    border: none;
    color: var(--text-secondary, #aaa);
    font-size: 0.76rem;
    cursor: pointer;
    padding: 0.2rem 0;
    margin: 0.1rem 0 0.4rem;
    text-align: left;
  }
  .advanced-toggle:hover { color: var(--text, #f0f0f0); }
  .detected-pills { display: flex; gap: 0.3rem; flex-wrap: wrap; }
  .pill {
    padding: 0.18rem 0.55rem;
    border-radius: 99px;
    border: 1px solid var(--accent, #4a7ef5);
    background: rgba(74,126,245,0.1);
    color: var(--accent, #4a7ef5);
    font-size: 0.72rem;
    cursor: pointer;
  }
  .pill:hover { background: rgba(74,126,245,0.2); }

  /* ── tool block ── */
  .tool-block {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding: 0.5rem 0.6rem;
    border: 1px solid var(--border, #333);
    border-radius: 6px;
    background: var(--surface-elevated, #1e1e1e);
  }
  .tool-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .tool-name {
    font-size: 0.82rem;
    font-weight: 600;
    color: var(--text-secondary, #ccc);
  }
  .tool-field {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
  }
  .field-label {
    font-size: 0.72rem;
    color: var(--text-muted, #888);
  }
  .path-row {
    display: flex;
    gap: 0.3rem;
    align-items: center;
  }
  .path-row .setting-input-full { flex: 1; width: auto; }
  .browse-btn {
    flex-shrink: 0;
    padding: 0.28rem 0.55rem;
    border-radius: 5px;
    border: 1px solid var(--border, #555);
    background: var(--input-bg, #2a2a2a);
    color: var(--text-secondary, #ccc);
    font-size: 0.85rem;
    cursor: pointer;
    white-space: nowrap;
  }
  .browse-btn:hover { background: var(--hover, #3a3a3a); color: var(--text, #f0f0f0); }

  .setting-input-full {
    width: 100%;
    box-sizing: border-box;
    padding: 0.28rem 0.5rem;
    border-radius: 5px;
    border: 1px solid var(--border, #555);
    background: var(--input-bg, #1a1a1a);
    color: var(--text, #f0f0f0);
    font-size: 0.78rem;
    font-family: ui-monospace, Consolas, monospace;
    outline: none;
  }
  .setting-input-full:focus { border-color: var(--accent, #4a7ef5); }
  .arg-hint {
    font-size: 0.68rem;
    color: var(--text-muted, #666);
    font-family: ui-monospace, Consolas, monospace;
  }

  /* ── reference table ── */
  .ref-toggle {
    background: none;
    border: none;
    color: var(--text-muted, #666);
    font-size: 0.72rem;
    cursor: pointer;
    text-align: left;
    padding: 0;
  }
  .ref-toggle:hover { color: var(--text-secondary, #aaa); }
  .ref-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.68rem;
    font-family: ui-monospace, Consolas, monospace;
    color: var(--text-secondary, #aaa);
  }
  .ref-table th {
    text-align: left;
    padding: 0.2rem 0.4rem;
    border-bottom: 1px solid var(--border, #333);
    color: var(--text-muted, #666);
    font-weight: 600;
  }
  .ref-table td {
    padding: 0.2rem 0.4rem;
    vertical-align: top;
    border-bottom: 1px solid var(--border, #252525);
  }
  .ref-table code {
    color: #aaa;
  }
  .row-right {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .btn-check-now {
    padding: 3px 10px;
    background: var(--surface-elevated, #2a2a2a);
    color: var(--text, #ccc);
    border: 1px solid var(--border, #444);
    border-radius: 4px;
    cursor: pointer;
    font-size: 12px;
  }

  .btn-check-now:hover:not(:disabled) {
    background: var(--hover, #333);
  }

  .btn-check-now:disabled {
    opacity: 0.5;
    cursor: default;
  }

  .check-ok {
    font-size: 12px;
    color: #5a9e5a;
  }

  .check-found {
    font-size: 12px;
    color: #e8a838;
  }

  .ref-note {
    font-family: inherit;
    font-size: 0.64rem;
    color: var(--text-muted, #555);
    font-style: italic;
  }

  .modal-footer {
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: 0.5rem;
    padding: 0.65rem 1rem;
    border-top: 1px solid var(--border, #3a3a3a);
    flex-shrink: 0;
  }
  .version-badge {
    flex: 1;
    font-size: 0.72rem;
    color: var(--text-muted, #888);
    font-family: ui-monospace, monospace;
    user-select: text;
  }
  .cancel-btn {
    padding: 0.35rem 0.9rem;
    border-radius: 6px;
    border: 1px solid var(--border, #555);
    background: none;
    color: var(--text-secondary, #aaa);
    font-size: 0.83rem;
    cursor: pointer;
  }
  .cancel-btn:hover { background: var(--hover, #3a3a3a); color: var(--text, #f0f0f0); }
  .save-btn {
    padding: 0.35rem 1.1rem;
    border-radius: 6px;
    border: none;
    background: var(--accent, #4a7ef5);
    color: #fff;
    font-size: 0.83rem;
    font-weight: 600;
    cursor: pointer;
  }
  .save-btn:hover { opacity: 0.85; }

  .theme-seg {
    display: flex;
    border: 1px solid var(--border, #555);
    border-radius: 5px;
    overflow: hidden;
  }
  .seg-btn {
    padding: 0.28rem 0.75rem;
    background: none;
    border: none;
    color: var(--text-muted, #888);
    font-size: 0.8rem;
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .seg-btn + .seg-btn { border-left: 1px solid var(--border, #555); }
  .seg-btn.active {
    background: var(--accent, #4a7ef5);
    color: #fff;
  }
  .seg-btn:not(.active):hover { background: var(--hover, #3a3a3a); color: var(--text, #f0f0f0); }
</style>
