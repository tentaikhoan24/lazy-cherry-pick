<script lang="ts">
  import type { FileDiffResult } from "./rpc-types";
  import FileDiff from "./FileDiff.svelte";
  import ConflictThreePane from "./ConflictThreePane.svelte";

  interface Props {
    file: string;
    diffResult: FileDiffResult | null;
    originalText: string;  // conflict-marker text (backup, before AI resolved it)
    resolvedText: string;  // AI-written merge result
    showEol: boolean;
    busy: boolean;
    onaccept: () => void;
    ondiscard: () => void;
    onclose: () => void;
  }

  let { file, diffResult, originalText, resolvedText, showEol, busy, onaccept, ondiscard, onclose }: Props = $props();

  let viewMode = $state<"diff" | "3way">("3way");

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") onclose();
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="overlay" onclick={onclose} role="presentation">
  <div class="modal" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true" aria-label="AI conflict resolution review">
    <div class="modal-header">
      <span class="icon">🤖</span>
      <span class="title">AI resolution — review</span>
      <code class="file">{file}</code>
      <div class="view-toggle" role="group" aria-label="View mode">
        <button class:active={viewMode === "diff"} onclick={() => (viewMode = "diff")}>Diff</button>
        <button class:active={viewMode === "3way"} onclick={() => (viewMode = "3way")}>3-way</button>
      </div>
      <button class="close-btn" onclick={onclose} aria-label="Close">✕</button>
    </div>

    <div class="diff-area">
      {#if viewMode === "3way"}
        <ConflictThreePane
          conflictText={originalText}
          mergedText={resolvedText}
          mergedLabel="🤖 Merge result (AI resolved)"
        />
      {:else}
        <FileDiff
          diff={diffResult?.diff ?? ""}
          file={null}
          loading={diffResult === null}
          onback={onclose}
          leftLabel="Conflict (original)"
          rightLabel="AI resolved"
          initialShowEol={showEol}
        />
      {/if}
    </div>

    <div class="footer">
      {#if viewMode === "3way"}
        <span class="hint">Theirs = cherry-picked commit · Ours = target branch · bottom = what the AI wrote. Accept to stage it, or discard to restore the conflict markers.</span>
      {:else}
        <span class="hint">Left = original conflict · Right = what the AI wrote. Accept to stage it, or discard to restore the conflict markers.</span>
      {/if}
      <div class="actions">
        <button class="discard-btn" onclick={ondiscard} disabled={busy}>Discard</button>
        <button class="accept-btn" onclick={onaccept} disabled={busy}>{busy ? "Working…" : "Accept & stage"}</button>
      </div>
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
    z-index: 1000;
  }
  .modal {
    width: 92vw;
    height: 88vh;
    max-width: 1400px;
    background: var(--surface, #252525);
    border: 1px solid var(--border, #3a3a3a);
    border-radius: 8px;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .modal-header {
    display: flex;
    align-items: center;
    gap: 0.55rem;
    padding: 0.55rem 0.8rem;
    border-bottom: 1px solid var(--border, #3a3a3a);
    background: var(--toolbar-bg, #252525);
    flex-shrink: 0;
  }
  .icon { font-size: 0.95rem; }
  .title { font-weight: 600; font-size: 0.9rem; color: var(--text, #f0f0f0); }
  .file {
    font-family: ui-monospace, monospace;
    font-size: 0.78rem;
    color: var(--accent, #4a7ef5);
    background: rgba(74, 126, 245, 0.12);
    padding: 0.1rem 0.45rem;
    border-radius: 4px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .close-btn {
    margin-left: auto;
    background: none;
    border: none;
    color: var(--text-muted, #888);
    font-size: 1rem;
    cursor: pointer;
    padding: 0.1rem 0.35rem;
  }
  .close-btn:hover { color: var(--text, #f0f0f0); }

  .view-toggle {
    display: flex;
    gap: 0.15rem;
    margin-left: auto;
    border: 1px solid var(--border, #3a3a3a);
    border-radius: 5px;
    overflow: hidden;
    flex-shrink: 0;
  }
  .view-toggle button {
    padding: 0.18rem 0.65rem;
    font-size: 0.72rem;
    background: transparent;
    border: none;
    color: var(--text-secondary, #aaa);
    cursor: pointer;
  }
  .view-toggle button.active {
    background: var(--accent, #4a7ef5);
    color: #fff;
  }
  .view-toggle button:not(.active):hover {
    background: var(--hover, rgba(255, 255, 255, 0.08));
  }

  .diff-area { flex: 1; min-height: 0; overflow: hidden; display: flex; }
  .diff-area :global(> *) { flex: 1; min-height: 0; }

  .footer {
    display: flex;
    align-items: center;
    gap: 1rem;
    padding: 0.55rem 0.8rem;
    border-top: 1px solid var(--border, #3a3a3a);
    background: var(--toolbar-bg, #252525);
    flex-shrink: 0;
  }
  .hint { font-size: 0.72rem; color: var(--text-muted, #888); flex: 1; }
  .actions { display: flex; gap: 0.5rem; }

  .discard-btn {
    padding: 0.35rem 0.9rem;
    border-radius: 5px;
    border: 1px solid #ef5350;
    background: transparent;
    color: #ef5350;
    font-size: 0.82rem;
    cursor: pointer;
  }
  .discard-btn:not(:disabled):hover { background: rgba(239, 83, 80, 0.12); }
  .discard-btn:disabled { opacity: 0.4; cursor: not-allowed; }

  .accept-btn {
    padding: 0.35rem 1rem;
    border-radius: 5px;
    border: none;
    background: #396cd8;
    color: #fff;
    font-size: 0.82rem;
    font-weight: 600;
    cursor: pointer;
  }
  .accept-btn:not(:disabled):hover { opacity: 0.88; }
  .accept-btn:disabled { opacity: 0.4; cursor: not-allowed; }
</style>
