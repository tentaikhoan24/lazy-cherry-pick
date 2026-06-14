<script lang="ts">
  import { onMount, tick } from "svelte";
  import { rpc } from "$lib/rpc";
  import { emit } from "@tauri-apps/api/event";
  import { getCurrentWindow } from "@tauri-apps/api/window";
  import { parseConflict, buildRenderLines, normalizeConflictText, type Part, type ConflictPart } from "$lib/conflict-parse";

  // ── Drag-select state (independent per pane) ──────────────────
  interface PaneSel { ci: number; startIdx: number; endIdx: number }
  let leftSel  = $state<PaneSel | null>(null);
  let rightSel = $state<PaneSel | null>(null);
  let draggingSide: "left" | "right" | null = null;

  // ── Context menu ───────────────────────────────────────────────
  interface CtxMenu {
    x: number; y: number;
    side: "left" | "right"; ci: number;
    selLines: string[];
    otherSelLines: string[];
  }
  let ctxMenu = $state<CtxMenu | null>(null);

  // ── Core state ─────────────────────────────────────────────────
  let repo = $state("");
  let file = $state("");
  let loading = $state(true);
  let error = $state("");
  let applying = $state(false);
  let saved = $state(false);
  let showRaw = $state(false);
  let rawEdited = false;       // non-reactive: track if user typed anything in raw mode
  let mergedViewRef = $state<HTMLElement | null>(null);

  let parts = $state<Part[]>([]);
  let mergedText = $state("");   // kept with conflict markers; only finalized at save
  let currentConflict = $state(0);

  // ── Provisional choices: soft resolution, changeable until save ─
  let provisionalChoices = $state<Map<number, string[]>>(new Map());

  // ── Resizable divider ──────────────────────────────────────────
  let topFlex = $state(55);
  let resizingH = false;
  let resizeStartY = 0;
  let resizeStartFlex = 0;

  // ── Merged display items ───────────────────────────────────────
  type MergedItem =
    | { kind: "normal";   text: string }
    | { kind: "resolved"; ci: number; lines: string[] }
    | { kind: "conflict"; ci: number; oursLines: string[]; theirsLines: string[] };

  function computeMergedDisplay(text: string, choices: Map<number, string[]>): MergedItem[] {
    const lines = text.split("\n");
    const out: MergedItem[] = [];
    let st: "n" | "o" | "t" = "n";
    let ci = -1;
    let oursLines: string[] = [];
    let theirsLines: string[] = [];
    for (const ln of lines) {
      if (st === "n" && ln.startsWith("<<<<<<<")) {
        ci++; oursLines = []; st = "o";
      } else if (st === "o" && ln.startsWith("=======")) {
        theirsLines = []; st = "t";
      } else if (st === "t" && ln.startsWith(">>>>>>>")) {
        const choice = choices.get(ci);
        if (choice !== undefined) {
          out.push({ kind: "resolved", ci, lines: choice });
        } else {
          out.push({ kind: "conflict", ci, oursLines: [...oursLines], theirsLines: [...theirsLines] });
        }
        st = "n";
      } else if (st === "o") { oursLines.push(ln); }
      else if (st === "t") { theirsLines.push(ln); }
      else { out.push({ kind: "normal", text: ln }); }
    }
    return out;
  }

  const mergedDisplay = $derived(computeMergedDisplay(mergedText, provisionalChoices));

  let leftPane  = $state<HTMLElement | null>(null);
  let rightPane = $state<HTMLElement | null>(null);
  let syncingScroll = false;

  // ── Derived ────────────────────────────────────────────────────
  const conflicts      = $derived(parts.filter((p): p is ConflictPart => p.kind === "conflict"));
  const totalConflicts = $derived(conflicts.length);
  const hasUnresolved  = $derived(provisionalChoices.size < totalConflicts);

  // ── Render lines (logic shared via $lib/conflict-parse) ────────
  const rendered = $derived(buildRenderLines(parts));

  // ── Provisional set — soft resolution, always changeable ──────
  function provisionalSet(ci: number, lines: string[]) {
    const m = new Map(provisionalChoices);
    m.set(ci, lines);
    provisionalChoices = m;
    ctxMenu = null;
    leftSel = null;
    rightSel = null;
    saved = false;
  }

  // ── Per-block inline actions ───────────────────────────────────
  function blockUseTheirs(ci: number) {
    const cp = conflicts[ci]; if (!cp) return;
    provisionalSet(ci, [...cp.theirs]);
    advanceConflict(ci);
  }
  function blockUseOurs(ci: number) {
    const cp = conflicts[ci]; if (!cp) return;
    provisionalSet(ci, [...cp.ours]);
    advanceConflict(ci);
  }
  function blockUseBoth(ci: number, order: "to" | "ot" = "to") {
    const cp = conflicts[ci]; if (!cp) return;
    provisionalSet(ci, order === "to" ? [...cp.theirs, ...cp.ours] : [...cp.ours, ...cp.theirs]);
    advanceConflict(ci);
  }

  // ── Selection helpers ──────────────────────────────────────────
  function getSelLines(side: "left" | "right", ci: number): string[] {
    const sel = side === "left" ? leftSel : rightSel;
    if (!sel || sel.ci !== ci) return [];
    const pane = side === "left" ? rendered.left : rendered.right;
    const wantKind = side === "left" ? "theirs" : "ours";
    const min = Math.min(sel.startIdx, sel.endIdx);
    const max = Math.max(sel.startIdx, sel.endIdx);
    const result: string[] = [];
    for (let i = min; i <= max && i < pane.length; i++) {
      const ln = pane[i];
      if (ln && ln.conflictIdx === ci && ln.kind === wantKind && ln.text !== null)
        result.push(ln.text);
    }
    return result;
  }

  function isLineSelected(idx: number, side: "left" | "right"): boolean {
    const sel = side === "left" ? leftSel : rightSel;
    if (!sel) return false;
    const min = Math.min(sel.startIdx, sel.endIdx);
    const max = Math.max(sel.startIdx, sel.endIdx);
    return idx >= min && idx <= max;
  }

  // ── Mouse handlers (drag-select in top panes) ─────────────────
  function onLineDown(e: MouseEvent, side: "left" | "right", idx: number, ci: number) {
    if (e.button !== 0 || ci < 0) return;
    e.preventDefault();
    currentConflict = ci;
    draggingSide = side;
    const sel: PaneSel = { ci, startIdx: idx, endIdx: idx };
    if (side === "left") leftSel = sel; else rightSel = sel;
  }
  function onLineMove(e: MouseEvent, side: "left" | "right", idx: number, ci: number) {
    if (draggingSide !== side) return;
    const sel = side === "left" ? leftSel : rightSel;
    if (!sel || sel.ci !== ci) return;
    const updated = { ...sel, endIdx: idx };
    if (side === "left") leftSel = updated; else rightSel = updated;
  }
  function onLineCtx(e: MouseEvent, side: "left" | "right", idx: number, ci: number) {
    e.preventDefault();
    if (ci < 0) return;
    currentConflict = ci;
    draggingSide = null;
    const thisSel = side === "left" ? leftSel : rightSel;
    if (!thisSel || thisSel.ci !== ci) {
      const sel: PaneSel = { ci, startIdx: idx, endIdx: idx };
      if (side === "left") leftSel = sel; else rightSel = sel;
    }
    const selLines      = getSelLines(side, ci);
    const otherSelLines = getSelLines(side === "left" ? "right" : "left", ci);
    ctxMenu = { x: e.clientX, y: e.clientY, side, ci, selLines, otherSelLines };
  }

  // ── Context menu actions ───────────────────────────────────────
  function menuUseSelected() {
    if (!ctxMenu || ctxMenu.selLines.length === 0) return;
    provisionalSet(ctxMenu.ci, ctxMenu.selLines);
    advanceConflict(ctxMenu.ci);
  }
  function menuUseWhole(side: "left" | "right") {
    if (!ctxMenu) return;
    const ci = ctxMenu.ci;
    provisionalSet(ci, side === "left" ? [...conflicts[ci].theirs] : [...conflicts[ci].ours]);
    advanceConflict(ci);
  }
  function menuUseCombined(order: "lt" | "tl") {
    if (!ctxMenu) return;
    const ci = ctxMenu.ci;
    const cp = conflicts[ci];
    provisionalSet(ci, order === "lt" ? [...cp.theirs, ...cp.ours] : [...cp.ours, ...cp.theirs]);
    advanceConflict(ci);
  }
  function menuUseCrossPane(order: "this-other" | "other-this") {
    if (!ctxMenu) return;
    const lines = order === "this-other"
      ? [...ctxMenu.selLines, ...ctxMenu.otherSelLines]
      : [...ctxMenu.otherSelLines, ...ctxMenu.selLines];
    provisionalSet(ctxMenu.ci, lines);
    advanceConflict(ctxMenu.ci);
  }

  function advanceConflict(ci: number) {
    if (ci < totalConflicts - 1) setTimeout(() => { currentConflict = ci + 1; scrollToConflict(ci + 1); }, 60);
  }

  // ── Toolbar navigation ─────────────────────────────────────────
  function prevConflict() {
    if (currentConflict > 0) { currentConflict--; scrollToConflict(currentConflict); leftSel = null; rightSel = null; }
  }
  function nextConflict() {
    if (currentConflict < totalConflicts - 1) { currentConflict++; scrollToConflict(currentConflict); leftSel = null; rightSel = null; }
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

  // ── Scroll sync ────────────────────────────────────────────────
  function onLeftScroll() {
    if (syncingScroll || !rightPane || !leftPane) return;
    syncingScroll = true; rightPane.scrollTop = leftPane.scrollTop; syncingScroll = false;
  }
  function onRightScroll() {
    if (syncingScroll || !leftPane || !rightPane) return;
    syncingScroll = true; leftPane.scrollTop = rightPane.scrollTop; syncingScroll = false;
  }

  // ── Resizable H-divider ────────────────────────────────────────
  function onHDividerDown(e: MouseEvent) {
    resizingH = true;
    resizeStartY = e.clientY;
    resizeStartFlex = topFlex;
    e.preventDefault();
  }

  // ── Raw mode: collect edited text from DOM then sync back to visual ─
  function getRawFinalText(): string {
    if (!mergedViewRef) return buildFinalText();
    const editables = Array.from(
      mergedViewRef.querySelectorAll<HTMLElement>(".mv-text[contenteditable], .mv-resolved-text[contenteditable]")
    );
    let ei = 0;
    const out: string[] = [];
    for (const item of mergedDisplay) {
      if (item.kind === "normal") {
        const t = editables[ei++]?.textContent ?? item.text;
        out.push(t === "​" ? "" : t);
      } else if (item.kind === "resolved") {
        for (const line of item.lines) {
          const t = editables[ei++]?.textContent ?? line;
          out.push(t === "​" ? "" : t);
        }
      }
      // conflict items: skip — they stay as unresolved in the final text
    }
    return out.join("\n");
  }

  function onRawLineInput(e: Event) {
    (e.currentTarget as HTMLElement).dataset.edited = "1";
    rawEdited = true;
    saved = false;
  }

  // ── Toggle raw mode ────────────────────────────────────────────
  function toggleRaw() {
    if (!showRaw) {
      rawEdited = false;
      showRaw = true;
    } else {
      // Only sync back if user actually typed something in raw mode
      if (rawEdited) {
        const edited = getRawFinalText();
        mergedText = edited;
        parts = parseConflict(edited);
        provisionalChoices = new Map();
      }
      showRaw = false;
    }
  }

  // ── Build final text: apply all provisional choices ────────────
  function buildFinalText(): string {
    let ci = 0;
    const lines = mergedText.split("\n");
    const out: string[] = [];
    let st: "n" | "o" | "t" = "n";
    for (const ln of lines) {
      if (st === "n" && ln.startsWith("<<<<<<<")) { st = "o"; }
      else if (st === "o" && ln.startsWith("=======")) { st = "t"; }
      else if (st === "t" && ln.startsWith(">>>>>>>")) {
        out.push(...(provisionalChoices.get(ci) ?? []));
        ci++; st = "n";
      } else if (st === "o" || st === "t") { /* skip */ }
      else { out.push(ln); }
    }
    return out.join("\n");
  }

  // ── Save & Stage ───────────────────────────────────────────────
  async function save() {
    applying = true; error = "";
    try {
      const finalText = showRaw ? getRawFinalText() : buildFinalText();
      await rpc.git.writeAndStageFile(repo, file, finalText);
      await emit("conflict-file-resolved", { file });
      saved = true;
      rawEdited = false;
    } catch (e) { error = String(e); }
    finally { applying = false; }
  }

  // ── Mount ──────────────────────────────────────────────────────
  onMount(() => {
    // Async init — fire and forget so cleanup function can be returned synchronously.
    (async () => {
      const p = new URLSearchParams(window.location.search);
      repo = p.get("repo") ?? "";
      file = p.get("file") ?? "";
      if (!repo || !file) { loading = false; error = "Missing parameters"; return; }
      try { const s = await rpc.settings.load(); document.body.classList.toggle("light", s.theme === "light"); } catch { /* ignore */ }
      try {
        const r = await rpc.git.fileContent(repo, file);
        parts = parseConflict(r.content);
        mergedText = normalizeConflictText(r.content);
      } catch (e) { error = String(e); }
      loading = false;
      setTimeout(() => scrollToConflict(0), 120);
    })();

    function onGlobalUp() { draggingSide = null; resizingH = false; }
    function onGlobalMove(e: MouseEvent) {
      if (!resizingH) return;
      const editorEl = document.querySelector(".merge-editor");
      const h = editorEl?.clientHeight ?? window.innerHeight;
      const dy = e.clientY - resizeStartY;
      topFlex = Math.max(20, Math.min(80, resizeStartFlex + (dy / h) * 100));
    }
    function onKeyDown(e: KeyboardEvent) {
      if (e.target instanceof HTMLTextAreaElement || e.target instanceof HTMLInputElement) return;
      if (e.key === "ArrowUp"   && !e.shiftKey) { e.preventDefault(); prevConflict(); }
      if (e.key === "ArrowDown" && !e.shiftKey) { e.preventDefault(); nextConflict(); }
    }
    window.addEventListener("mouseup", onGlobalUp);
    window.addEventListener("mousemove", onGlobalMove);
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("mouseup", onGlobalUp);
      window.removeEventListener("mousemove", onGlobalMove);
      window.removeEventListener("keydown", onKeyDown);
    };
  });
</script>

<!-- ── Context menu overlay ──────────────────────────────────── -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
{#if ctxMenu}
  {@const thisSide   = ctxMenu.side}
  {@const otherSide  = ctxMenu.side === "left" ? "right" : "left"}
  {@const thisLabel  = thisSide  === "left" ? "Theirs" : "Ours"}
  {@const otherLabel = otherSide === "left" ? "Theirs" : "Ours"}
  {@const hasCross   = ctxMenu.selLines.length > 0 && ctxMenu.otherSelLines.length > 0}
  <div class="ctx-overlay" onmousedown={() => ctxMenu = null}></div>
  <div class="ctx-menu" style="left: {ctxMenu.x}px; top: {ctxMenu.y}px">

    {#if ctxMenu.selLines.length > 0}
      {@const fullBlock = thisSide === "left" ? conflicts[ctxMenu.ci].theirs : conflicts[ctxMenu.ci].ours}
      {#if ctxMenu.selLines.length < fullBlock.length}
        <button class="ctx-item" onclick={menuUseSelected}>
          Use selected lines from {thisLabel} <span class="ctx-badge">{ctxMenu.selLines.length} line{ctxMenu.selLines.length === 1 ? "" : "s"}</span>
        </button>
        <div class="ctx-sep"></div>
      {/if}
    {/if}

    <button class="ctx-item" onclick={() => menuUseWhole(thisSide)}>
      Use entire block from {thisLabel}
    </button>
    <button class="ctx-item" onclick={() => menuUseWhole(otherSide)}>
      Use entire block from {otherLabel}
    </button>
    <div class="ctx-sep"></div>
    <button class="ctx-item" onclick={() => menuUseCombined(thisSide === "left" ? "lt" : "tl")}>
      {thisLabel} first · {otherLabel} after
    </button>
    <button class="ctx-item" onclick={() => menuUseCombined(thisSide === "left" ? "tl" : "lt")}>
      {otherLabel} first · {thisLabel} after
    </button>

    {#if hasCross}
      <div class="ctx-sep"></div>
      <div class="ctx-hint">Combine selected lines from both panes:</div>
      <button class="ctx-item ctx-cross" onclick={() => menuUseCrossPane("this-other")}>
        {thisLabel} selected ({ctxMenu.selLines.length}) first · {otherLabel} ({ctxMenu.otherSelLines.length}) after
      </button>
      <button class="ctx-item ctx-cross" onclick={() => menuUseCrossPane("other-this")}>
        {otherLabel} selected ({ctxMenu.otherSelLines.length}) first · {thisLabel} ({ctxMenu.selLines.length}) after
      </button>
    {/if}
  </div>
{/if}

<!-- ── Main layout ────────────────────────────────────────────── -->
<div class="merge-editor">

  <!-- Toolbar -->
  <div class="toolbar">
    <button class="tb-btn nav" onclick={prevConflict} disabled={currentConflict === 0} title="Previous conflict (↑)">▲</button>
    <button class="tb-btn nav" onclick={nextConflict} disabled={currentConflict >= totalConflicts - 1} title="Next conflict (↓)">▼</button>
    <span class="counter">{totalConflicts > 0 ? `${currentConflict + 1} / ${totalConflicts}` : "–"}</span>

    <div class="tb-sep"></div>
    <button class="tb-btn btn-theirs" onclick={() => blockUseTheirs(currentConflict)} disabled={totalConflicts === 0} title="Use all Theirs">← Theirs</button>
    <button class="tb-btn btn-ours"   onclick={() => blockUseOurs(currentConflict)}   disabled={totalConflicts === 0} title="Use all Ours">Ours →</button>
    <button class="tb-btn btn-both"   onclick={() => blockUseBoth(currentConflict)}   disabled={totalConflicts === 0} title="Combine: Theirs + Ours">← + →</button>

    <div class="tb-flex-gap"></div>

    <span class="file-label" title={file}>{file}</span>
    <div class="tb-sep"></div>

    {#if hasUnresolved}
      {@const pendingCount = totalConflicts - provisionalChoices.size}
      <span class="badge-warn">⚠ {pendingCount} unresolved</span>
    {:else if saved}
      <span class="badge-saved">✓ Saved</span>
    {:else}
      <span class="badge-ok">✓ Ready to save</span>
    {/if}

    <div class="tb-sep"></div>
    <button class="tb-btn btn-save" onclick={save} disabled={hasUnresolved || applying || saved}>
      {applying ? "Saving…" : "Save & Stage"}
    </button>
    <button class="tb-btn btn-close" onclick={() => getCurrentWindow().close()}>✕</button>
  </div>

  {#if error}
    <div class="error-bar">{error}</div>
  {/if}

  {#if loading}
    <div class="state-msg">Loading…</div>
  {:else if totalConflicts === 0}
    <div class="state-msg">No conflict markers found.</div>
  {:else}
    <!-- ── Top panes ── -->
    <div class="top-panes" style="flex: {topFlex} 1 0">

      <!-- Left: THEIRS (CHERRY_PICK_HEAD) -->
      <div class="pane" bind:this={leftPane} onscroll={onLeftScroll}>
        <div class="pane-hdr theirs-hdr">CHERRY_PICK_HEAD &nbsp;·&nbsp; Theirs</div>
        {#each rendered.left as ln, i}
          {#if ln.kind === "conflict-header"}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <div
              class="ch-bar"
              class:ch-active={ln.conflictIdx === currentConflict}
              class:ch-resolved={provisionalChoices.has(ln.conflictIdx)}
              data-ci={ln.conflictIdx}
              onmousedown={() => { currentConflict = ln.conflictIdx; leftSel = null; rightSel = null; }}
            >
              <span class="ch-num">Conflict {ln.conflictIdx + 1} / {totalConflicts}</span>
              {#if provisionalChoices.has(ln.conflictIdx)}
                <span class="ch-done-badge">✓</span>
              {/if}
              <button class="ch-btn ch-theirs" onclick={(e) => { e.stopPropagation(); blockUseTheirs(ln.conflictIdx); }}>← Theirs</button>
              <button class="ch-btn ch-both"   onclick={(e) => { e.stopPropagation(); blockUseBoth(ln.conflictIdx); }}>T+O</button>
            </div>
          {:else}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <div
              class="line-row row-{ln.kind}"
              class:row-active={ln.conflictIdx === currentConflict && ln.kind !== "context"}
              class:row-sel={isLineSelected(i, "left") && ln.kind === "theirs"}
              onmousedown={e => onLineDown(e, "left", i, ln.conflictIdx)}
              onmousemove={e => onLineMove(e, "left", i, ln.conflictIdx)}
              oncontextmenu={e => onLineCtx(e, "left", i, ln.conflictIdx)}
            >
              <span class="gutter">{ln.lineNum ?? ""}</span>
              <span class="lc">{ln.text ?? ""}</span>
            </div>
          {/if}
        {/each}
      </div>

      <div class="v-divider"></div>

      <!-- Right: OURS (HEAD) -->
      <div class="pane" bind:this={rightPane} onscroll={onRightScroll}>
        <div class="pane-hdr ours-hdr">HEAD &nbsp;·&nbsp; Ours</div>
        {#each rendered.right as ln, i}
          {#if ln.kind === "conflict-header"}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <div
              class="ch-bar"
              class:ch-active={ln.conflictIdx === currentConflict}
              class:ch-resolved={provisionalChoices.has(ln.conflictIdx)}
              data-ci={ln.conflictIdx}
              onmousedown={() => { currentConflict = ln.conflictIdx; leftSel = null; rightSel = null; }}
            >
              <button class="ch-btn ch-ours" onclick={(e) => { e.stopPropagation(); blockUseOurs(ln.conflictIdx); }}>Ours →</button>
              <button class="ch-btn ch-both" onclick={(e) => { e.stopPropagation(); blockUseBoth(ln.conflictIdx, "ot"); }}>O+T</button>
            </div>
          {:else}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <div
              class="line-row row-{ln.kind}"
              class:row-active={ln.conflictIdx === currentConflict && ln.kind !== "context"}
              class:row-sel={isLineSelected(i, "right") && ln.kind === "ours"}
              onmousedown={e => onLineDown(e, "right", i, ln.conflictIdx)}
              onmousemove={e => onLineMove(e, "right", i, ln.conflictIdx)}
              oncontextmenu={e => onLineCtx(e, "right", i, ln.conflictIdx)}
            >
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

    <!-- ── Bottom: Merged result ── -->
    <div class="bottom-pane" style="flex: {100 - topFlex} 1 0">
      <div class="pane-hdr merged-hdr">
        <span>Merge result</span>
        <button class="raw-toggle" onclick={toggleRaw}>
          {showRaw ? "◧ Visual" : "✎ Raw"}
        </button>
      </div>

      <div class="merged-view" bind:this={mergedViewRef}>
          {#each mergedDisplay as item}
            {#if item.kind === "normal"}
              <div class="mv-line">
                <span class="mv-gutter"></span>
                <!-- svelte-ignore a11y_no_static_element_interactions -->
                <span
                  class="mv-text"
                  contenteditable={showRaw ? "true" : undefined}
                  spellcheck="false"
                  oninput={showRaw ? onRawLineInput : undefined}
                >{item.text || "​"}</span>
              </div>
            {:else if item.kind === "resolved"}
              <!-- svelte-ignore a11y_no_static_element_interactions -->
              <div
                class="mv-resolved"
                class:mv-resolved-active={item.ci === currentConflict}
                onmousedown={showRaw ? undefined : () => { currentConflict = item.ci; scrollToConflict(item.ci); }}
              >
                {#if !showRaw}
                  <div class="mv-resolved-hdr">
                    <span class="mv-resolved-icon">✓</span>
                    <span class="mv-resolved-label">Conflict {item.ci + 1} / {totalConflicts}</span>
                    <button class="mv-act mv-chg-theirs" onclick={(e) => { e.stopPropagation(); blockUseTheirs(item.ci); }}>← Theirs</button>
                    <button class="mv-act mv-chg-ours"   onclick={(e) => { e.stopPropagation(); blockUseOurs(item.ci); }}>Ours →</button>
                    <button class="mv-act mv-chg-both"   onclick={(e) => { e.stopPropagation(); blockUseBoth(item.ci); }}>T+O</button>
                  </div>
                {/if}
                {#each item.lines as line}
                  <div class="mv-resolved-line">
                    <span class="mv-resolved-gutter"></span>
                    <!-- svelte-ignore a11y_no_static_element_interactions -->
                    <span
                      class="mv-resolved-text"
                      contenteditable={showRaw ? "true" : undefined}
                      spellcheck="false"
                      oninput={showRaw ? onRawLineInput : undefined}
                    >{line || "​"}</span>
                  </div>
                {/each}
              </div>
            {:else}
              <!-- Unresolved conflict — hatched red block -->
              <!-- svelte-ignore a11y_no_static_element_interactions -->
              <div
                class="mv-conflict"
                class:mv-cf-active={item.ci === currentConflict}
                onmousedown={() => { currentConflict = item.ci; scrollToConflict(item.ci); }}
              >
                <div class="mv-cf-hdr">
                  <span class="mv-cf-icon">⚠</span>
                  <span class="mv-cf-label">Conflict {item.ci + 1} / {totalConflicts}</span>
                  <span class="mv-cf-counts">{item.oursLines.length}↑ · {item.theirsLines.length}↓</span>
                  <button class="mv-act mv-use-ours"   onclick={(e) => { e.stopPropagation(); blockUseOurs(item.ci); }}>Ours ({item.oursLines.length})</button>
                  <button class="mv-act mv-use-theirs" onclick={(e) => { e.stopPropagation(); blockUseTheirs(item.ci); }}>Theirs ({item.theirsLines.length})</button>
                  <button class="mv-act mv-use-both"   onclick={(e) => { e.stopPropagation(); blockUseBoth(item.ci); }}>T+O</button>
                </div>
                {#each { length: Math.max(item.oursLines.length, item.theirsLines.length, 1) } as _, _r}
                  <div class="mv-cf-row">
                    <span class="mv-gutter mv-cf-gutter">!</span>
                    <span class="mv-hatch"></span>
                  </div>
                {/each}
              </div>
            {/if}
          {/each}
        </div>
    </div>
  {/if}
</div>

<style>
  :global(*) { box-sizing: border-box; margin: 0; padding: 0; }
  :global(body) {
    background: #1a1a1a; color: #f0f0f0;
    font-family: Inter, Avenir, Helvetica, Arial, sans-serif;
    font-size: 13px; overflow: hidden;
  }

  /* ── Context menu ─────────────────────────────────────────── */
  .ctx-overlay { position: fixed; inset: 0; z-index: 99; }
  .ctx-menu {
    position: fixed; z-index: 100;
    background: #2d2d2d; border: 1px solid #555;
    border-radius: 6px; padding: 0.25rem 0;
    min-width: 220px; box-shadow: 0 6px 20px rgba(0,0,0,0.6);
    font-size: 0.82rem;
  }
  .ctx-item {
    display: flex; align-items: center; justify-content: space-between;
    width: 100%; padding: 0.32rem 0.9rem;
    background: none; border: none; color: #e0e0e0;
    text-align: left; cursor: pointer; gap: 0.5rem;
  }
  .ctx-item:hover { background: rgba(74,126,245,0.25); color: #fff; }
  .ctx-badge {
    font-size: 0.7rem; background: rgba(74,126,245,0.3); color: #90caf9;
    padding: 0.05rem 0.35rem; border-radius: 3px; white-space: nowrap;
  }
  .ctx-sep { height: 1px; background: #444; margin: 0.2rem 0; }
  .ctx-hint { padding: 0.15rem 0.9rem; font-size: 0.68rem; color: #666; }
  .ctx-cross { color: #c8a030; }
  .ctx-cross:hover { background: rgba(200,160,48,0.2); color: #e8c040; }

  /* ── Main layout ──────────────────────────────────────────── */
  .merge-editor {
    position: fixed; inset: 0;
    display: flex; flex-direction: column; overflow: hidden;
  }

  /* ── Light theme overrides (toolbar chrome only; editor panes stay dark) ── */
  :global(body.light) .toolbar { background: #ffffff; border-bottom-color: #d0d0d0; }
  :global(body.light) .tb-btn { background: #eeeeee; border-color: #d0d0d0; color: #444; }
  :global(body.light) .tb-btn.btn-save:disabled { background: #eeeeee; border-color: #d0d0d0; color: #aaa; }
  :global(body.light) .tb-btn.btn-close { color: #aaa; }
  :global(body.light) .tb-btn.btn-close:hover { color: #444; }
  :global(body.light) .tb-sep { background: #d0d0d0; }
  :global(body.light) .counter { color: #888; }
  :global(body.light) .file-label { color: #666; }
  :global(body.light) .error-bar { background: rgba(239,83,80,.06); }
  :global(body.light) .state-msg { color: #aaa; }
  :global(body.light) .raw-toggle { background: #eeeeee; border-color: #d0d0d0; color: #888; }
  :global(body.light) .raw-toggle:hover { color: #444; border-color: #aaa; }
  :global(body.light) .ctx-menu { background: #ffffff; border-color: #d0d0d0; box-shadow: 0 4px 16px rgba(0,0,0,0.15); }
  :global(body.light) .ctx-item { color: #1a1a1a; }
  :global(body.light) .ctx-item:hover { background: #e8e8e8; }
  :global(body.light) .ctx-sep { background: #d0d0d0; }
  :global(body.light) .ctx-hint { color: #888; }

  /* ── Toolbar ──────────────────────────────────────────────── */
  .toolbar {
    display: flex; align-items: center; gap: 0.3rem;
    padding: 0.3rem 0.6rem;
    background: #252525; border-bottom: 1px solid #333;
    flex-shrink: 0; min-height: 36px;
  }
  .tb-sep { width: 1px; height: 18px; background: #3a3a3a; flex-shrink: 0; }
  .tb-flex-gap { flex: 1; }
  .counter {
    font-size: 0.75rem; color: #888;
    font-family: ui-monospace, monospace;
    min-width: 40px; text-align: center;
  }
  .file-label {
    font-size: 0.73rem; color: #aaa;
    font-family: ui-monospace, monospace;
    max-width: 260px; overflow: hidden;
    text-overflow: ellipsis; white-space: nowrap;
  }
  .badge-warn  { font-size: 0.72rem; color: #ffa726; white-space: nowrap; }
  .badge-ok    { font-size: 0.72rem; color: #66bb6a; white-space: nowrap; }
  .badge-saved { font-size: 0.72rem; color: #4a7ef5; white-space: nowrap; }

  .tb-btn {
    padding: 0.2rem 0.55rem;
    border-radius: 4px; border: 1px solid #3a3a3a;
    background: #2a2a2a; color: #bbb;
    font-size: 0.76rem; cursor: pointer; white-space: nowrap;
  }
  .tb-btn:disabled { opacity: 0.35; cursor: not-allowed; }
  .tb-btn.nav { padding: 0.2rem 0.5rem; font-size: 0.7rem; }
  .tb-btn.btn-theirs:not(:disabled):hover { background: rgba(74,126,245,0.15); border-color: rgba(74,126,245,0.4); color: #90caf9; }
  .tb-btn.btn-ours:not(:disabled):hover   { background: rgba(102,187,106,0.15); border-color: rgba(102,187,106,0.4); color: #a5d6a7; }
  .tb-btn.btn-both:not(:disabled):hover   { background: rgba(255,167,38,0.15); border-color: rgba(255,167,38,0.4); color: #ffa726; }
  .tb-btn.btn-save {
    background: #2c5cc5; color: #fff; border-color: transparent; font-weight: 600;
  }
  .tb-btn.btn-save:not(:disabled):hover { background: #1e4aaa; }
  .tb-btn.btn-save:disabled { background: #252525; border-color: #3a3a3a; color: #555; }
  .tb-btn.btn-close { background: transparent; border-color: transparent; color: #555; font-size: 0.8rem; }
  .tb-btn.btn-close:hover { color: #ddd; }

  .error-bar {
    flex-shrink: 0; padding: 0.3rem 0.75rem;
    background: rgba(239,83,80,.08); border-bottom: 1px solid #ef5350;
    color: #ef5350; font-size: 0.78rem;
  }
  .state-msg {
    flex: 1; display: flex; align-items: center; justify-content: center;
    color: #555; font-size: 0.85rem;
  }

  /* ── Top panes ────────────────────────────────────────────── */
  .top-panes {
    min-height: 0; display: flex; overflow: hidden;
  }
  .pane {
    flex: 1; min-width: 0;
    overflow-y: scroll; overflow-x: auto;
    font-family: ui-monospace, 'Cascadia Code', Consolas, monospace;
    font-size: 12px; line-height: 19px;
    user-select: none;
  }

  /* Pane header (sticky) */
  .pane-hdr {
    position: sticky; top: 0; z-index: 2;
    padding: 0.15rem 0.5rem 0.15rem 0;
    font-size: 0.68rem; font-weight: 700; letter-spacing: 0.04em;
    border-bottom: 1px solid;
    display: flex; align-items: center; gap: 6px;
  }
  .theirs-hdr { color: #5b8def; background: #1a2035; border-bottom-color: #273a5e; }
  .ours-hdr   { color: #5cb85c; background: #1a2a1a; border-bottom-color: #2a4a2a; }
  .merged-hdr {
    color: #999; background: #1e1e1e; border-bottom-color: #2e2e2e;
    display: flex; align-items: center; gap: 6px;
  }
  .merged-hdr span { padding-left: 0.5rem; }

  /* V-divider */
  .v-divider { width: 3px; background: #252525; flex-shrink: 0; cursor: col-resize; }
  .v-divider:hover { background: #4a7ef5; }

  /* ── Inline conflict header bar ───────────────────────────── */
  .ch-bar {
    display: flex; align-items: center; gap: 5px;
    height: 20px; padding: 0 6px;
    background: #181820;
    border-top: 2px solid #333348;
    border-bottom: 1px solid #28283a;
    cursor: pointer; flex-shrink: 0;
  }
  .ch-bar.ch-active {
    background: #181c30;
    border-top-color: #3a5aaa;
    border-bottom-color: #2a3a6a;
  }
  /* Resolved conflict header — orange tint */
  .ch-bar.ch-resolved {
    border-top-color: #5a4010;
    border-bottom-color: #3a2808;
    background: #141008;
  }
  .ch-bar.ch-resolved.ch-active {
    background: #181200;
    border-top-color: #8a6820;
  }
  .ch-num {
    font-size: 0.67rem; color: #4a4a60;
    font-family: ui-monospace, monospace; flex: 1;
    letter-spacing: 0.02em;
  }
  .ch-bar.ch-active .ch-num { color: #6080cc; }
  .ch-bar.ch-resolved .ch-num { color: #6a5020; }
  .ch-bar.ch-resolved.ch-active .ch-num { color: #c09040; }

  .ch-done-badge {
    font-size: 0.63rem; color: #8a6020; font-weight: 700;
    padding: 0 2px;
  }
  .ch-bar.ch-active .ch-done-badge { color: #c09040; }

  .ch-btn {
    padding: 0 6px; border-radius: 3px;
    font-size: 0.66rem; cursor: pointer; white-space: nowrap;
    border: 1px solid transparent; line-height: 15px; height: 15px;
  }
  .ch-theirs { background: rgba(58,98,200,0.15); color: #90b8ff; border-color: rgba(58,98,200,0.3); }
  .ch-theirs:hover { background: rgba(58,98,200,0.35); color: #c0d8ff; }
  .ch-ours   { background: rgba(58,138,58,0.15); color: #90cc90; border-color: rgba(58,138,58,0.3); }
  .ch-ours:hover   { background: rgba(58,138,58,0.35); color: #b0e8b0; }
  .ch-both   { background: rgba(180,130,20,0.1); color: #b89040; border-color: rgba(180,130,20,0.2); }
  .ch-both:hover   { background: rgba(180,130,20,0.25); }

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

  .row-sel {
    background: rgba(255,200,70,0.2) !important;
    border-left-color: #e8b840 !important;
    outline: 1px solid rgba(255,200,70,0.35);
    outline-offset: -1px;
  }

  /* ── H-divider (resizable) ────────────────────────────────── */
  .h-divider {
    height: 5px; flex-shrink: 0;
    background: #252525; cursor: ns-resize;
    border-top: 1px solid #1a1a1a; border-bottom: 1px solid #1a1a1a;
  }
  .h-divider:hover { background: #4a7ef5; }

  /* ── Bottom pane ──────────────────────────────────────────── */
  .bottom-pane {
    min-height: 0; display: flex; flex-direction: column; overflow: hidden;
  }
  .raw-toggle {
    margin-left: auto; margin-right: 6px;
    padding: 1px 8px; border-radius: 3px;
    border: 1px solid #333; background: #222; color: #666;
    font-size: 0.68rem; cursor: pointer;
  }
  .raw-toggle:hover { color: #aaa; border-color: #555; }

  /* ── Merged visual view ───────────────────────────────────── */
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
    background: #111; border-right: 1px solid #1e1e1e;
    flex-shrink: 0;
  }
  .mv-text { flex: 1; padding: 0 8px; white-space: pre; tab-size: 4; color: #c0c0c0; }
  .mv-text[contenteditable] { outline: none; cursor: text; }
  .mv-text[data-edited]     { background: rgba(255,220,0,0.15); color: #fff176; }

  /* ── Provisionally resolved conflict block (orange) ───────── */
  .mv-resolved {
    border-left: 3px solid #b07020;
    border-top: 1px solid #7a4e10;
    border-bottom: 1px solid #7a4e10;
    background: #171200;
    cursor: pointer;
  }
  .mv-resolved.mv-resolved-active {
    border-left-color: #e09030;
    border-top-color: #aa6820;
    border-bottom-color: #aa6820;
    background: #201800;
  }
  .mv-resolved:hover { filter: brightness(1.1); }

  .mv-resolved-hdr {
    display: flex; align-items: center; gap: 5px;
    padding: 1px 6px; min-height: 20px;
    background: #221600;
    border-bottom: 1px solid #332000;
    font-size: 0.67rem;
  }
  .mv-resolved-icon  { color: #d08030; font-size: 0.75rem; }
  .mv-resolved-label { color: #a06820; font-weight: 600; flex: 1; }
  .mv-resolved.mv-resolved-active .mv-resolved-label { color: #e09030; }

  .mv-resolved-line {
    display: flex; align-items: stretch; min-height: 19px;
  }
  .mv-resolved-gutter {
    min-width: 40px; width: 40px;
    background: #1a1000; border-right: 1px solid #2a1a00;
    flex-shrink: 0;
  }
  .mv-resolved-text {
    flex: 1; padding: 0 8px;
    white-space: pre; tab-size: 4;
    color: #d4a050; line-height: 19px;
  }
  .mv-resolved-text[contenteditable] { outline: none; cursor: text; }
  .mv-resolved-text[data-edited]     { background: rgba(255,220,0,0.20); color: #fff176; }

  /* Change buttons in resolved block */
  .mv-act {
    padding: 0 6px; border-radius: 3px;
    font-size: 0.64rem; cursor: pointer; white-space: nowrap;
    border: 1px solid transparent; line-height: 15px;
  }
  .mv-act:hover { filter: brightness(1.3); }
  .mv-chg-theirs { background: rgba(58,98,200,0.2);   color: #90b8ff; border-color: rgba(58,98,200,0.35); }
  .mv-chg-ours   { background: rgba(58,138,58,0.2);   color: #80cc80; border-color: rgba(58,138,58,0.35); }
  .mv-chg-both   { background: rgba(180,130,20,0.15); color: #c8a030; border-color: rgba(180,130,20,0.3); }

  /* ── Unresolved conflict block (hatched red) ──────────────── */
  .mv-conflict {
    border-left: 3px solid #6b2020;
    border-top: 1px solid #4a1818;
    border-bottom: 1px solid #4a1818;
    background: #1a0f0f;
    cursor: pointer;
  }
  .mv-conflict.mv-cf-active {
    border-left-color: #cc3030;
    border-top-color: #882020;
    border-bottom-color: #882020;
    background: #200f0f;
  }
  .mv-conflict:hover { filter: brightness(1.1); }

  .mv-cf-hdr {
    display: flex; align-items: center; gap: 5px;
    padding: 1px 6px; min-height: 20px;
    background: #2a1010; border-bottom: 1px solid #3a1a1a;
    font-size: 0.67rem;
  }
  .mv-cf-icon  { color: #cc4040; font-size: 0.75rem; }
  .mv-cf-label { color: #aa3030; font-weight: 600; flex: 1; }
  .mv-conflict.mv-cf-active .mv-cf-label { color: #e05050; }
  .mv-cf-counts { color: #663030; font-size: 0.63rem; font-family: ui-monospace, monospace; }

  .mv-cf-row { display: flex; align-items: stretch; min-height: 19px; }
  .mv-cf-gutter { color: #6b2020 !important; background: #1f0808 !important; border-right-color: #3a1010 !important; }
  .mv-hatch {
    flex: 1;
    background: repeating-linear-gradient(
      135deg,
      rgba(180,30,30,0.08) 0px, rgba(180,30,30,0.08) 4px,
      rgba(100,10,10,0.15) 4px, rgba(100,10,10,0.15) 8px
    );
  }
  .mv-use-ours   { background: rgba(58,138,58,0.2);   color: #80cc80; border-color: rgba(58,138,58,0.35); }
  .mv-use-theirs { background: rgba(58,98,200,0.2);   color: #90b8ff; border-color: rgba(58,98,200,0.35); }
  .mv-use-both   { background: rgba(180,130,20,0.15); color: #c8a030; border-color: rgba(180,130,20,0.3); }
</style>
