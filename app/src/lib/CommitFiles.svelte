<script lang="ts">
  import type { CommitFile } from "./rpc-types";

  interface Props {
    files: CommitFile[];
    loading: boolean;
    selectedPath: string;
    onselect: (file: CommitFile) => void;
    // ── M11c — partial-file pick ──────────────────────────────
    /** Target branch the partial pick lands on (for the button label). */
    target?: string;
    /** True while a partial pick is in flight. */
    picking?: boolean;
    /** Apply only the given files of this commit onto the target. */
    onpartialpick?: (keep: string[]) => void;
  }

  let { files, loading, selectedPath, onselect, target = "", picking = false, onpartialpick }: Props = $props();

  // M11c — which files are ticked for a partial pick.
  let checked = $state(new Set<string>());
  // Reset the selection whenever the file list changes (new commit selected).
  $effect(() => { files; checked = new Set(); });

  function toggleCheck(path: string) {
    const next = new Set(checked);
    if (next.has(path)) next.delete(path); else next.add(path);
    checked = next;
  }
  const allChecked = $derived(files.length > 0 && checked.size === files.length);
  function toggleAll() {
    checked = allChecked ? new Set() : new Set(files.map((f) => f.path));
  }

  const statusColor: Record<string, string> = {
    A: "#66bb6a",
    D: "#ef5350",
    M: "#ffa726",
    R: "#42a5f5",
    C: "#ab47bc",
    T: "#26c6da",
    U: "#ef5350",
  };

  function statusLabel(s: string): string {
    return ({ A: "A", D: "D", M: "M", R: "R", C: "C", T: "T", U: "U" })[s] ?? s;
  }

  const totalAdded = $derived(files.reduce((s, f) => s + f.added, 0));
  const totalRemoved = $derived(files.reduce((s, f) => s + f.removed, 0));
</script>

<div class="panel">
  <div class="panel-header">
    Files changed
    {#if files.length > 0}
      <span class="summary">
        {files.length} file{files.length === 1 ? "" : "s"}
        <span class="added">+{totalAdded}</span>
        <span class="removed">-{totalRemoved}</span>
      </span>
    {/if}
  </div>

  {#if loading}
    <div class="empty">Loading…</div>
  {:else if files.length === 0}
    <div class="empty">No files changed.</div>
  {:else}
    <div class="file-list">
      {#each files as f}
        <div
          class="file-row"
          class:active={selectedPath === f.path}
          role="row"
          tabindex="0"
          onclick={(e) => { if (!(e.target as HTMLElement).closest(".cb-wrap")) onselect(f); }}
          onkeydown={(e) => e.key === "Enter" && onselect(f)}
          title="Click to view diff"
        >
          <label class="cb-wrap" title="Select for partial pick">
            <input type="checkbox" checked={checked.has(f.path)} tabindex="-1" onchange={() => toggleCheck(f.path)} />
          </label>
          <span class="status-badge" style="color: {statusColor[f.status] ?? '#aaa'}">{statusLabel(f.status)}</span>
          <span class="file-path">{f.path}</span>
          <span class="stats">
            {#if f.added > 0}<span class="added">+{f.added}</span>{/if}
            {#if f.removed > 0}<span class="removed">-{f.removed}</span>{/if}
          </span>
        </div>
      {/each}
    </div>
    {#if onpartialpick && files.length > 0}
      <div class="partial-footer">
        <label class="select-all" title="Select all files">
          <input type="checkbox" checked={allChecked} onchange={toggleAll} />
          {checked.size > 0 ? `${checked.size} selected` : "Select files"}
        </label>
        <button
          class="partial-btn"
          disabled={checked.size === 0 || picking || checked.size === files.length}
          title={checked.size === files.length ? "All files selected — use the normal Apply instead" : `Apply only the ${checked.size} selected file(s) onto ${target}`}
          onclick={() => onpartialpick?.([...checked])}
        >
          {picking ? "Picking…" : `Pick ${checked.size || ""} file${checked.size === 1 ? "" : "s"} → ${target}`}
        </button>
      </div>
    {/if}
  {/if}
</div>

<style>
  .panel {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
    background: var(--input-bg, #1e1e1e);
  }
  .panel-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.35rem 0.75rem;
    font-size: 0.72rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-secondary, #aaa);
    border-bottom: 1px solid var(--border, #3a3a3a);
    flex-shrink: 0;
  }
  .summary {
    font-weight: 400;
    text-transform: none;
    letter-spacing: 0;
    display: flex;
    gap: 0.35rem;
    font-family: ui-monospace, monospace;
  }
  .empty {
    padding: 1rem;
    font-size: 0.82rem;
    color: var(--text-muted, #666);
    text-align: center;
  }
  .file-list {
    flex: 1;
    overflow-y: auto;
    padding: 0.2rem 0;
  }
  .file-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.3rem 0.75rem;
    font-size: 0.8rem;
    border-bottom: 1px solid var(--border-subtle, #2a2a2a);
  }
  .file-row { cursor: pointer; }
  .file-row:hover { background: var(--hover, #252525); }
  .file-row.active { background: var(--selected, #1a2a4a); outline: 1px solid var(--accent, #4a7ef5); outline-offset: -1px; }
  .status-badge {
    flex-shrink: 0;
    font-family: ui-monospace, monospace;
    font-weight: 700;
    font-size: 0.72rem;
    width: 1rem;
    text-align: center;
  }
  .file-path {
    flex: 1;
    font-family: ui-monospace, monospace;
    color: var(--text, #f0f0f0);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .stats {
    flex-shrink: 0;
    display: flex;
    gap: 0.35rem;
    font-family: ui-monospace, monospace;
    font-size: 0.75rem;
  }
  .added { color: #66bb6a; }
  .removed { color: #ef5350; }

  /* M11c — partial-file pick */
  .cb-wrap {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    padding: 0.1rem;
    cursor: pointer;
  }
  .cb-wrap input { cursor: pointer; margin: 0; }
  .partial-footer {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.4rem 0.75rem;
    border-top: 1px solid var(--border, #3a3a3a);
  }
  .select-all {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    font-size: 0.74rem;
    color: var(--text-secondary, #aaa);
    cursor: pointer;
    white-space: nowrap;
  }
  .partial-btn {
    margin-left: auto;
    padding: 0.3rem 0.7rem;
    border-radius: 5px;
    border: 1px solid var(--accent, #4a7ef5);
    background: rgba(74, 126, 245, 0.12);
    color: var(--accent, #4a7ef5);
    font-size: 0.76rem;
    cursor: pointer;
    white-space: nowrap;
  }
  .partial-btn:not(:disabled):hover { background: rgba(74, 126, 245, 0.22); }
  .partial-btn:disabled { opacity: 0.4; cursor: not-allowed; }
</style>
