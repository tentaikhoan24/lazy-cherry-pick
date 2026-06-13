<script lang="ts">
  import { invoke } from "@tauri-apps/api/core";
  import { getVersion } from "@tauri-apps/api/app";
  import { open as openDialog } from "@tauri-apps/plugin-dialog";
  import type { AppSettings, DetectedTool, ForgeConnection } from "./rpc-types";

  interface Props {
    settings: AppSettings;
    onclose: () => void;
    onsave: (s: AppSettings) => void;
    onchecknow?: () => Promise<boolean>;
    /** M14 — current repo's forge connection, null if not configured. */
    forgeConnection?: ForgeConnection | null;
    /** True when a repo is open — controls availability of Connect/Disconnect. */
    forgeConnectAvailable?: boolean;
    onconnectforge?: () => void;
    ondisconnectforge?: () => void;
  }

  let { settings, onclose, onsave, onchecknow, forgeConnection = null, forgeConnectAvailable = false, onconnectforge, ondisconnectforge }: Props = $props();

  let maxCommits = $state(settings.maxCommits);
  let defaultApplyMode = $state(settings.defaultApplyMode);
  let showEolMarkers = $state(settings.showEolMarkers);
  let autoFetchOnOpen = $state(settings.autoFetchOnOpen);
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
      maxCommits, defaultApplyMode, showEolMarkers, autoFetchOnOpen, theme,
      externalDiffEnabled, externalDiffPath, externalDiffArgs,
      externalMergeEnabled, externalMergePath, externalMergeArgs,
      checkForUpdatesOnStartup,
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
</script>

<svelte:window onkeydown={onKeydown} />

<div class="overlay" onclick={onclose} role="presentation">
  <div class="modal" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true" aria-label="Settings">
    <div class="modal-header">
      <span class="modal-title">Settings</span>
      <button class="close-btn" onclick={onclose} aria-label="Close">✕</button>
    </div>

    <div class="modal-body">
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

      <!-- M14 — Connected accounts -->
      <div class="section-sep">Connected accounts</div>

      <div class="row">
        <label class="label">Forge (for PR creation)</label>
        <div class="forge-info">
          {#if !forgeConnectAvailable}
            <span class="hint">Open a repo to configure a forge connection.</span>
          {:else if forgeConnection}
            <span class="connected-label">
              <strong>{forgeConnection.kind === "github" ? "GitHub" : forgeConnection.kind === "gitlab" ? "GitLab" : "Bitbucket"}</strong>
              · {forgeConnection.host}
              · <span class="mono">{forgeConnection.username}</span>
            </span>
            <button class="forge-action danger" onclick={() => ondisconnectforge?.()}>
              Disconnect
            </button>
            <button class="forge-action" onclick={() => onconnectforge?.()}>
              Reconnect
            </button>
          {:else}
            <span class="hint">No connection for this repo.</span>
            <button class="forge-action primary" onclick={() => onconnectforge?.()}>
              Connect…
            </button>
          {/if}
        </div>
      </div>

      <!-- External Tools -->
      <div class="section-sep">External Tools</div>

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
    </div>

    <div class="modal-footer">
      <span class="version-badge" title="Installed version">
        Lazy Cherry Pick {appVersion ? `v${appVersion}` : ""}
      </span>
      <button class="cancel-btn" onclick={onclose}>Cancel</button>
      <button class="save-btn" onclick={save}>Save</button>
    </div>
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
    background: var(--surface, #252525);
    border: 1px solid var(--border, #3a3a3a);
    border-radius: 10px;
    width: 460px;
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
    padding: 0.75rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    overflow-y: auto;
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

  /* ── section separator ── */
  .section-sep {
    font-size: 0.72rem;
    font-weight: 600;
    color: var(--text-muted, #666);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    padding-top: 0.25rem;
    border-top: 1px solid var(--border, #3a3a3a);
    margin-top: 0.25rem;
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
