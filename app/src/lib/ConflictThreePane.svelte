<script lang="ts">
  import { onMount, tick } from "svelte";
  import { parseConflict, buildRenderLines, normalizeConflictText, type ConflictPart } from "./conflict-parse";

  interface Props {
    /** Raw conflict-marker text (still containing <<<<<<< / ======= / >>>>>>>). */
    conflictText: string;
    /** The merged result to show in the bottom pane (marker-free). */
    mergedText: string;
    theirsLabel?: string;
    oursLabel?: string;
    mergedLabel?: string;
  }

  let {
    conflictText,
    mergedText,
    theirsLabel = "CHERRY_PICK_HEAD  ·  Theirs",
    oursLabel = "HEAD  ·  Ours",
    mergedLabel = "Merge result",
  }: Props = $props();

  const parsedParts    = $derived(parseConflict(conflictText));
  const rendered       = $derived(buildRenderLines(parsedParts));
  const conflicts      = $derived(parsedParts.filter((p): p is ConflictPart => p.kind === "conflict"));
  const totalConflicts = $derived(conflicts.length);
  const mergedLines    = $derived(normalizeConflictText(mergedText).split("\n"));

  let currentConflict = $state(0);

  // ── Resizable H-divider ───────────────────────────────────────
  let topFlex = $state(55);
  let resizingH = false;
  let resizeStartY = 0;
  let resizeStartFlex = 0;
  let rootEl = $state<HTMLElement | null>(null);
  function onHDividerDown(e: MouseEvent) {
    resizingH = true;
    resizeStartY = e.clientY;
    resizeStartFlex = topFlex;
    e.preventDefault();
  }

  // ── Scroll sync (top two panes) ───────────────────────────────
  let leftPane  = $state<HTMLElement | null>(null);
  let rightPane = $state<HTMLElement | null>(null);
  let syncingScroll = false;
  function onLeftScroll() {
    if (syncingScroll || !rightPane || !leftPane) return;
    syncingScroll = true; rightPane.scrollTop = leftPane.scrollTop; syncingScroll = false;
  }
  function onRightScroll() {
    if (syncingScroll || !leftPane || !rightPane) return;
    syncingScroll = true; leftPane.scrollTop = rightPane.scrollTop; syncingScroll = false;
  }

  // ── Conflict navigation ───────────────────────────────────────
  export function prevConflict() {
    if (currentConflict > 0) { currentConflict--; scrollToConflict(currentConflict); }
  }
  export function nextConflict() {
    if (currentConflict < totalConflicts - 1) { currentConflict++; scrollToConflict(currentConflict); }
  }
  async function scrollToConflict(idx: number) {
    await tick();
    if (!leftPane) return;
    const el = leftPane.querySelector(`[data-ci="${idx}"]`) as HTMLElement | null;
    if (!el) return;
    const pRect = leftPane.getBoundingClientRect();
    const eRect = el.getBoundingClientRect();
    const top = eRect.top - pRect.top + leftPane.scrollTop - 32;
    leftPane.scrollTop = Math.max(0, top);
    if (rightPane) rightPane.scrollTop = Math.max(0, top);
  }

  onMount(() => {
    function onGlobalUp() { resizingH = false; }
    function onGlobalMove(e: MouseEvent) {
      if (!resizingH) return;
      const h = rootEl?.clientHeight ?? window.innerHeight;
      const dy = e.clientY - resizeStartY;
      topFlex = Math.max(20, Math.min(80, resizeStartFlex + (dy / h) * 100));
    }
    window.addEventListener("mouseup", onGlobalUp);
    window.addEventListener("mousemove", onGlobalMove);
    return () => {
      window.removeEventListener("mouseup", onGlobalUp);
      window.removeEventListener("mousemove", onGlobalMove);
    };
  });
</script>

<div class="c3-root" bind:this={rootEl}>
  <!-- ── Toolbar: conflict navigation ── -->
  {#if totalConflicts > 0}
    <div class="c3-toolbar">
      <button class="nav-btn" onclick={prevConflict} disabled={currentConflict === 0} title="Previous conflict (↑)">▲</button>
      <button class="nav-btn" onclick={nextConflict} disabled={currentConflict >= totalConflicts - 1} title="Next conflict (↓)">▼</button>
      <span class="nav-counter">{currentConflict + 1} / {totalConflicts}</span>
    </div>
  {/if}

  <!-- ── Top: Theirs / Ours panes ── -->
  <div class="top-panes" style="flex: {topFlex} 1 0">
    <!-- Left: THEIRS (CHERRY_PICK_HEAD) -->
    <div class="pane" bind:this={leftPane} onscroll={onLeftScroll}>
      <div class="pane-hdr theirs-hdr">{theirsLabel}</div>
      {#each rendered.left as ln}
        {#if ln.kind === "conflict-header"}
          <div class="ch-bar" class:ch-active={ln.conflictIdx === currentConflict} data-ci={ln.conflictIdx}>
            <span class="ch-num">Conflict {ln.conflictIdx + 1} / {totalConflicts}</span>
          </div>
        {:else}
          <div class="line-row row-{ln.kind}" class:row-active={ln.conflictIdx === currentConflict && ln.kind !== "context"}>
            <span class="gutter">{ln.lineNum ?? ""}</span>
            <span class="lc">{ln.text ?? ""}</span>
          </div>
        {/if}
      {/each}
    </div>

    <div class="v-divider"></div>

    <!-- Right: OURS (HEAD) -->
    <div class="pane" bind:this={rightPane} onscroll={onRightScroll}>
      <div class="pane-hdr ours-hdr">{oursLabel}</div>
      {#each rendered.right as ln}
        {#if ln.kind === "conflict-header"}
          <div class="ch-bar" class:ch-active={ln.conflictIdx === currentConflict} data-ci={ln.conflictIdx}></div>
        {:else}
          <div class="line-row row-{ln.kind}" class:row-active={ln.conflictIdx === currentConflict && ln.kind !== "context"}>
            <span class="gutter">{ln.lineNum ?? ""}</span>
            <span class="lc">{ln.text ?? ""}</span>
          </div>
        {/if}
      {/each}
    </div>
  </div>

  <!-- ── Resizable H-divider ── -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="h-divider" onmousedown={onHDividerDown} title="Drag to resize"></div>

  <!-- ── Bottom: merged result ── -->
  <div class="bottom-pane" style="flex: {100 - topFlex} 1 0">
    <div class="pane-hdr merged-hdr"><span>{mergedLabel}</span></div>
    <div class="merged-view">
      {#each mergedLines as line, i}
        <div class="mv-line">
          <span class="mv-gutter">{i + 1}</span>
          <span class="mv-text">{line || "​"}</span>
        </div>
      {/each}
    </div>
  </div>
</div>

<style>
  .c3-root {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    background: #1a1a1a;
  }

  /* ── Toolbar ──────────────────────────────────────────────── */
  .c3-toolbar {
    display: flex; align-items: center; gap: 0.3rem;
    padding: 0.25rem 0.55rem;
    background: #1e1e1e; border-bottom: 1px solid #2e2e2e;
    flex-shrink: 0;
  }
  .nav-btn {
    padding: 0.1rem 0.45rem;
    font-size: 0.65rem;
    background: #2a2a2a; border: 1px solid #3a3a3a;
    border-radius: 4px; color: #aaa; cursor: pointer;
  }
  .nav-btn:disabled { opacity: 0.35; cursor: not-allowed; }
  .nav-btn:not(:disabled):hover { color: #f0f0f0; }
  .nav-counter {
    font-size: 0.72rem; font-family: ui-monospace, monospace;
    color: #aaa; min-width: 38px; text-align: center;
  }

  /* ── Top panes ────────────────────────────────────────────── */
  .top-panes { min-height: 0; display: flex; overflow: hidden; }
  .pane {
    flex: 1; min-width: 0;
    overflow-y: scroll; overflow-x: auto;
    font-family: ui-monospace, 'Cascadia Code', Consolas, monospace;
    font-size: 12px; line-height: 19px;
    user-select: text;
  }
  .pane-hdr {
    position: sticky; top: 0; z-index: 2;
    padding: 0.15rem 0.5rem 0.15rem 0;
    font-size: 0.68rem; font-weight: 700; letter-spacing: 0.04em;
    border-bottom: 1px solid;
    display: flex; align-items: center; gap: 6px;
  }
  .theirs-hdr { color: #5b8def; background: #1a2035; border-bottom-color: #273a5e; }
  .ours-hdr   { color: #5cb85c; background: #1a2a1a; border-bottom-color: #2a4a2a; }
  .merged-hdr { color: #999; background: #1e1e1e; border-bottom-color: #2e2e2e; }
  .merged-hdr span { padding-left: 0.5rem; }

  .v-divider { width: 3px; background: #252525; flex-shrink: 0; }

  /* ── Inline conflict header bar ───────────────────────────── */
  .ch-bar {
    display: flex; align-items: center; gap: 5px;
    height: 20px; padding: 0 6px;
    background: #181820;
    border-top: 2px solid #333348;
    border-bottom: 1px solid #28283a;
    flex-shrink: 0;
  }
  .ch-bar.ch-active {
    background: #181c30;
    border-top-color: #3a5aaa;
    border-bottom-color: #2a3a6a;
  }
  .ch-num {
    font-size: 0.67rem; color: #4a4a60;
    font-family: ui-monospace, monospace; flex: 1;
    letter-spacing: 0.02em;
  }
  .ch-bar.ch-active .ch-num { color: #6080cc; }

  /* ── Line rows ────────────────────────────────────────────── */
  .line-row {
    display: flex; align-items: stretch;
    cursor: default; min-height: 19px;
    border-left: 2px solid transparent;
  }
  .gutter {
    min-width: 40px; width: 40px;
    padding: 0 5px 0 0; text-align: right;
    font-size: 10.5px; line-height: 19px; color: #3a3a3a;
    background: #141414; border-right: 1px solid #222;
    flex-shrink: 0; user-select: none;
  }
  .lc { flex: 1; padding: 0 8px; white-space: pre; tab-size: 4; line-height: 19px; }

  .row-context .gutter { color: #4a4a4a; }
  .row-context .lc     { color: #9a9a9a; }

  .row-theirs { border-left-color: #3a62c8; background: rgba(58,98,200,0.18); }
  .row-theirs .gutter { color: #5a7acc; background: rgba(58,98,200,0.12); border-right-color: #2a3a6a; }
  .row-theirs .lc     { color: #c8dcff; }

  .row-ours { border-left-color: #3a8a3a; background: rgba(58,138,58,0.18); }
  .row-ours .gutter { color: #5aaa5a; background: rgba(58,138,58,0.12); border-right-color: #1a4a1a; }
  .row-ours .lc     { color: #c0ecc0; }

  .row-filler { background: rgba(0,0,0,0.35); border-left-color: transparent; }
  .row-filler .gutter { background: rgba(0,0,0,0.25); border-right-color: #1a1a1a; }
  .row-filler .lc     { border-bottom: 1px dashed #282828; }

  .row-theirs.row-active { background: rgba(58,98,200,0.36); border-left-color: #6090ff; }
  .row-theirs.row-active .gutter { background: rgba(58,98,200,0.24); color: #8ab0ff; }
  .row-ours.row-active   { background: rgba(58,138,58,0.36); border-left-color: #60c060; }
  .row-ours.row-active .gutter   { background: rgba(58,138,58,0.24); color: #80cc80; }
  .row-filler.row-active { background: rgba(0,0,0,0.45); }

  /* ── H-divider (resizable) ────────────────────────────────── */
  .h-divider {
    height: 5px; flex-shrink: 0;
    background: #252525; cursor: ns-resize;
    border-top: 1px solid #1a1a1a; border-bottom: 1px solid #1a1a1a;
  }
  .h-divider:hover { background: #4a7ef5; }

  /* ── Bottom merge result ──────────────────────────────────── */
  .bottom-pane { min-height: 0; display: flex; flex-direction: column; overflow: hidden; }
  .merged-view {
    flex: 1; min-height: 0;
    overflow-y: auto; overflow-x: auto;
    font-family: ui-monospace, 'Cascadia Code', Consolas, monospace;
    font-size: 12px; line-height: 19px;
    background: #0f0f0f;
  }
  .mv-line {
    display: flex; align-items: stretch;
    min-height: 19px; border-left: 2px solid transparent;
  }
  .mv-gutter {
    min-width: 40px; width: 40px;
    padding: 0 5px 0 0; text-align: right;
    font-size: 10.5px; line-height: 19px; color: #3a3a3a;
    background: #111; border-right: 1px solid #1e1e1e;
    flex-shrink: 0; user-select: none;
  }
  .mv-text { flex: 1; padding: 0 8px; white-space: pre; tab-size: 4; color: #c0c0c0; line-height: 19px; }
</style>
