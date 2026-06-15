<script lang="ts">
  import type { Branch, Commit, CommitFilter } from "./rpc-types";
  import BranchSelect from "./BranchSelect.svelte";

  interface Props {
    branches: Branch[];
    sourceBranch: string;
    commits: Commit[];
    selected: Set<string>;
    selectedSha: string;
    loading: boolean;
    refreshing: boolean;
    applied: Set<string>;
    /** M12a — true if another page of commits may be loaded via `onloadmore`. */
    hasMore: boolean;
    /** M12a — a "load more" request is currently in flight. */
    loadingMore: boolean;
    /** M12b — number of new commits available on the upstream of `sourceBranch`. 0 = no badge. */
    newCommitsCount: number;
    onsourcebranch: (branch: string) => void;
    ontoggle: (sha: string) => void;
    onselect: (commit: Commit) => void;
    onfetch: () => void;
    onpull: () => void;
    onfilter: (filter: CommitFilter) => void;
    /** M12a — fetch and append the next page of commits. */
    onloadmore: () => void;
    /** M12b — pull/reload to bring in the new upstream commits. */
    onloadnew: () => void;
  }

  let { branches, sourceBranch, commits, selected, selectedSha, loading, refreshing, applied, hasMore, loadingMore, newCommitsCount, onsourcebranch, ontoggle, onselect, onfetch, onpull, onfilter, onloadmore, onloadnew }: Props = $props();

  // ── M12a — infinite scroll ─────────────────────────────────
  function onCommitListScroll(e: Event) {
    const el = e.currentTarget as HTMLElement;
    if (!hasMore || loadingMore || loading) return;
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 300) {
      onloadmore();
    }
  }

  let refreshDropdownOpen = $state(false);
  let refreshMode = $state<"fetch" | "pull">("fetch");

  const refreshLabel = $derived(refreshMode === "pull" ? "Pull" : "Fetch");

  function selectRefreshMode(m: "fetch" | "pull") {
    refreshMode = m;
    refreshDropdownOpen = false;
  }

  function executeRefresh() {
    if (refreshMode === "pull") onpull();
    else onfetch();
  }

  function toggleRefreshDropdown() {
    if (!refreshing && !loading) refreshDropdownOpen = !refreshDropdownOpen;
  }

  function onClickOutside(e: MouseEvent) {
    const target = e.target as HTMLElement;
    if (!target.closest(".refresh-wrap")) refreshDropdownOpen = false;
    if (!target.closest(".save-dropdown-wrap")) saveDropdownOpen = false;
    if (!target.closest(".preset-wrap")) presetsOpen = false;
  }

  function fmt(ts: number): string {
    const d = new Date(ts * 1000);
    return d.toLocaleString(undefined, {
      month: "short", day: "numeric", year: "numeric",
      hour: "2-digit", minute: "2-digit", second: "2-digit",
      hour12: false,
    });
  }

  function shortSha(sha: string) { return sha.slice(0, 7); }

  // ── filter bar ────────────────────────────────────────────
  let filterOpen = $state(false);
  let fAuthor = $state("");
  let fMessage = $state("");
  let fSince = $state("");
  let fUntil = $state("");
  let fPath = $state("");

  const activeCount = $derived(
    [fAuthor, fMessage, fSince, fUntil, fPath].filter(v => v.trim() !== "").length
  );

  const filtersDirtyFromPreset = $derived.by(() => {
    if (!activePresetName) return false;
    const p = presets.find(pr => pr.name === activePresetName);
    if (!p) return false;
    const pf = p.filter;
    return fAuthor.trim() !== (pf.author ?? "") ||
      fMessage.trim() !== (pf.messageContains ?? "") ||
      fSince.trim() !== (pf.since ?? "") ||
      fUntil.trim() !== (pf.until ?? "") ||
      fPath.trim() !== (pf.pathGlob ?? "");
  });

  function applyFilter() {
    const f: CommitFilter = {};
    if (fAuthor.trim())  f.author = fAuthor.trim();
    if (fMessage.trim()) f.messageContains = fMessage.trim();
    if (fSince.trim())   f.since = fSince.trim();
    if (fUntil.trim())   f.until = fUntil.trim();
    if (fPath.trim())    f.pathGlob = fPath.trim();
    onfilter(f);
  }

  function clearFilter() {
    fAuthor = ""; fMessage = ""; fSince = ""; fUntil = ""; fPath = "";
    activePresetName = null;
    onfilter({});
  }

  function onFilterKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") applyFilter();
  }

  // ── filter presets (M6b) ──────────────────────────────────
  const PRESETS_KEY = "lcp-filter-presets";
  type Preset = { name: string; filter: CommitFilter };

  let presets = $state<Preset[]>([]);
  let presetsOpen = $state(false);
  let saveNameInput = $state("");
  let savingPreset = $state(false);
  let activePresetName = $state<string | null>(null);
  let saveDropdownOpen = $state(false);

  $effect(() => {
    try { presets = JSON.parse(localStorage.getItem(PRESETS_KEY) ?? "[]"); } catch { presets = []; }
  });

  function persistPresets(p: Preset[]) {
    presets = p;
    localStorage.setItem(PRESETS_KEY, JSON.stringify(p));
  }

  function savePreset(overrideName?: string) {
    const name = overrideName ?? saveNameInput.trim();
    if (!name) return;
    const f: CommitFilter = {};
    if (fAuthor.trim())  f.author = fAuthor.trim();
    if (fMessage.trim()) f.messageContains = fMessage.trim();
    if (fSince.trim())   f.since = fSince.trim();
    if (fUntil.trim())   f.until = fUntil.trim();
    if (fPath.trim())    f.pathGlob = fPath.trim();
    persistPresets([...presets.filter(p => p.name !== name), { name, filter: f }]);
    activePresetName = name;
    saveNameInput = "";
    savingPreset = false;
    saveDropdownOpen = false;
  }

  function loadPreset(p: Preset) {
    fAuthor   = p.filter.author ?? "";
    fMessage  = p.filter.messageContains ?? "";
    fSince    = p.filter.since ?? "";
    fUntil    = p.filter.until ?? "";
    fPath     = p.filter.pathGlob ?? "";
    activePresetName = p.name;
    presetsOpen = false;
    onfilter(p.filter);
  }

  function deletePreset(name: string, e: MouseEvent) {
    e.stopPropagation();
    persistPresets(presets.filter(p => p.name !== name));
  }

  function onSaveKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") savePreset();
    else if (e.key === "Escape") { savingPreset = false; saveNameInput = ""; }
  }

  // ── commit badges (M6c) ───────────────────────────────────
  type Badge = { label: string; cls: string };

  const CC_RE = /^(feat|fix|docs|style|refactor|perf|test|build|ci|chore)(\([^)]*\))?!?:/i;
  const JIRA_RE = /^\[([A-Z][A-Z0-9]+-\d+)\]/;
  const BREAKING_RE = /^[a-z]+(\([^)]*\))?!:/i;

  function getBadge(subject: string): Badge | null {
    if (BREAKING_RE.test(subject)) {
      const m = subject.match(/^([a-z]+)/i);
      return { label: (m?.[1] ?? "feat").toLowerCase() + "!", cls: "badge-breaking" };
    }
    const jira = subject.match(JIRA_RE);
    if (jira) return { label: jira[1], cls: "badge-jira" };
    const cc = subject.match(CC_RE);
    if (!cc) return null;
    const type = cc[1].toLowerCase();
    const cls =
      type === "feat"                       ? "badge-feat"
      : type === "fix"                      ? "badge-fix"
      : type === "docs"                     ? "badge-docs"
      : type === "test"                     ? "badge-test"
      : type === "perf"                     ? "badge-perf"
      : /* chore|style|refactor|build|ci */ "badge-chore";
    return { label: type, cls };
  }

  function stripBadgePrefix(subject: string): string {
    return subject.replace(JIRA_RE, "").replace(CC_RE, "").trimStart();
  }

  // ── keyboard navigation ────────────────────────────────────
  let rowEls: (HTMLElement | undefined)[] = [];
</script>

<svelte:window onclick={onClickOutside} />

<div class="panel">
  <div class="panel-header">
    <label class="label">Source branch</label>
    <BranchSelect
      branches={branches}
      value={sourceBranch}
      disabled={branches.length === 0 || refreshing}
      onchange={onsourcebranch}
    />

    {#if newCommitsCount > 0}
      <button class="new-commits-badge" onclick={onloadnew} title="Fetch and load new commits">
        ↻ {newCommitsCount} new commit{newCommitsCount > 1 ? "s" : ""}
      </button>
    {/if}

    <!-- Filter toggle -->
    <button
      class="filter-btn"
      class:active={activeCount > 0}
      class:open={filterOpen}
      onclick={() => (filterOpen = !filterOpen)}
      title="Filter commits"
    >
      {#if activeCount > 0}
        Filter <span class="filter-badge">{activeCount}</span>
      {:else}
        Filter
      {/if}
    </button>

    <!-- Fetch / Pull dropdown -->
    <div class="refresh-wrap">
      <button
        class="refresh-btn"
        onclick={executeRefresh}
        disabled={refreshing || loading || branches.length === 0}
        title={refreshMode === "pull" ? "Pull source branch from remote" : "Fetch from remote"}
      >
        {refreshing ? "…" : refreshLabel}
      </button>
      <button
        class="refresh-arrow"
        onclick={toggleRefreshDropdown}
        disabled={refreshing || loading || branches.length === 0}
        aria-label="More refresh options"
      >▾</button>
      {#if refreshDropdownOpen}
        <ul class="refresh-menu">
          <li>
            <button class:active={refreshMode === "fetch"} onclick={() => selectRefreshMode("fetch")}>
              <span class="check">{refreshMode === "fetch" ? "✓" : ""}</span>
              Fetch
            </button>
          </li>
          <li>
            <button class:active={refreshMode === "pull"} onclick={() => selectRefreshMode("pull")}>
              <span class="check">{refreshMode === "pull" ? "✓" : ""}</span>
              Pull
            </button>
          </li>
        </ul>
      {/if}
    </div>
  </div>

  {#if filterOpen}
    <div class="filter-bar">
      <div class="filter-row">
        <label class="filter-label">Author</label>
        <input class="filter-input" type="text" placeholder="e.g. john" bind:value={fAuthor} onkeydown={onFilterKeydown} spellcheck={false} />
      </div>
      <div class="filter-row">
        <label class="filter-label">Message</label>
        <input class="filter-input" type="text" placeholder="keyword or regex" bind:value={fMessage} onkeydown={onFilterKeydown} spellcheck={false} />
      </div>
      <div class="filter-row">
        <label class="filter-label">Since</label>
        <input class="filter-input filter-date" type="text" placeholder="2024-01-01" bind:value={fSince} onkeydown={onFilterKeydown} spellcheck={false} />
        <label class="filter-label filter-label-mid">Until</label>
        <input class="filter-input filter-date" type="text" placeholder="2024-12-31" bind:value={fUntil} onkeydown={onFilterKeydown} spellcheck={false} />
      </div>
      <div class="filter-row">
        <label class="filter-label">Path</label>
        <input class="filter-input" type="text" placeholder="src/**.ts" bind:value={fPath} onkeydown={onFilterKeydown} spellcheck={false} />
      </div>
      <div class="filter-actions">
        <div class="preset-wrap">
          <button class="preset-toggle-btn" class:has-active={activePresetName !== null} onclick={() => (presetsOpen = !presetsOpen)}>
            {#if activePresetName}
              <span class="preset-active-name">{activePresetName}</span>
            {:else}
              Presets{presets.length > 0 ? ` (${presets.length})` : ""}
            {/if}
            ▾
          </button>
          {#if presetsOpen}
            <ul class="preset-menu">
              {#if presets.length === 0}
                <li class="preset-empty">No saved presets</li>
              {:else}
                {#each presets as p}
                  <li class="preset-item" onclick={() => loadPreset(p)}>
                    <span class="preset-name">{p.name}</span>
                    <button class="preset-del" onclick={(e) => deletePreset(p.name, e)} title="Delete preset">
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <polyline points="3 6 5 6 21 6"/>
                        <path d="M19 6l-1 14H6L5 6"/>
                        <path d="M10 11v6M14 11v6"/>
                        <path d="M9 6V4h6v2"/>
                      </svg>
                    </button>
                  </li>
                {/each}
              {/if}
            </ul>
          {/if}
        </div>

        {#if savingPreset}
          <input
            class="preset-name-input"
            type="text"
            placeholder="Preset name…"
            bind:value={saveNameInput}
            onkeydown={onSaveKeydown}
            spellcheck={false}
            autofocus
          />
          <button class="filter-apply-btn" onclick={() => savePreset()} disabled={!saveNameInput.trim()}>Save</button>
          <button class="filter-clear-btn" onclick={() => { savingPreset = false; saveNameInput = ""; }}>✕</button>
        {:else if activePresetName && filtersDirtyFromPreset}
          <div class="save-dropdown-wrap">
            <button class="preset-save-btn save-overwrite" onclick={() => savePreset(activePresetName ?? undefined)} title="Save changes to '{activePresetName}'">
              Save
            </button>
            <button class="save-arrow" onclick={() => (saveDropdownOpen = !saveDropdownOpen)} aria-label="More save options">▾</button>
            {#if saveDropdownOpen}
              <ul class="save-menu">
                <li><button onclick={() => savePreset(activePresetName ?? undefined)}>Save "{activePresetName}"</button></li>
                <li><button onclick={() => { saveDropdownOpen = false; savingPreset = true; saveNameInput = ""; }}>Save as…</button></li>
              </ul>
            {/if}
          </div>
          <button class="filter-apply-btn" onclick={applyFilter}>Apply</button>
          <button class="filter-clear-btn" onclick={clearFilter} disabled={activeCount === 0}>Clear</button>
        {:else}
          <button class="preset-save-btn" onclick={() => (savingPreset = true)} disabled={activeCount === 0} title="Save current filter as preset">
            + Save
          </button>
          <button class="filter-apply-btn" onclick={applyFilter}>Apply</button>
          <button class="filter-clear-btn" onclick={clearFilter} disabled={activeCount === 0}>Clear</button>
        {/if}
      </div>
    </div>
  {/if}

  <div class="commit-list" class:is-loading={loading} onscroll={onCommitListScroll}>
    {#if loading && commits.length === 0}
      <div class="empty">Loading commits…</div>
    {:else if commits.length === 0}
      <div class="empty">No commits found.</div>
    {:else}
      {#each commits as c, i}
        {@const badge = getBadge(c.subject)}
        {@const isApplied = applied.has(c.sha)}
        <div
          class="commit-row"
          class:checked={selected.has(c.sha)}
          class:active={selectedSha === c.sha}
          class:applied={isApplied}
          role="row"
          tabindex="0"
          bind:this={rowEls[i]}
          onclick={() => onselect(c)}
          onkeydown={(e) => {
            if (e.key === 'ArrowDown') { e.preventDefault(); rowEls[i + 1]?.focus(); }
            else if (e.key === 'ArrowUp') { e.preventDefault(); i > 0 && rowEls[i - 1]?.focus(); }
            else if (e.key === ' ' && !isApplied) { e.preventDefault(); ontoggle(c.sha); }
            else if (e.key === 'Enter') { onselect(c); }
          }}
        >
          <label
            class="cb-wrap"
            onclick={(e) => { e.stopPropagation(); }}
            title={isApplied ? "Already applied to target branch" : (selected.has(c.sha) ? "Remove from queue" : "Add to queue")}
          >
            <input
              type="checkbox"
              checked={selected.has(c.sha)}
              onchange={() => ontoggle(c.sha)}
              disabled={isApplied}
            />
          </label>
          <div class="commit-info">
            <span class="subject" title={c.subject}>
              {#if badge}<span class="badge {badge.cls}">{badge.label}</span>{/if}{stripBadgePrefix(c.subject)}
            </span>
            <span class="meta">
              <span class="sha">{shortSha(c.sha)}</span>
              <span class="author">{c.author}</span>
              <span class="date">{fmt(c.time)}</span>
            </span>
          </div>
          {#if isApplied}
            <span class="applied-badge">Applied</span>
          {/if}
        </div>
      {/each}
      {#if loadingMore}
        <div class="loading-more">Loading more…</div>
      {/if}
    {/if}
  </div>
</div>

<style>
  .panel {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
    border-right: 1px solid var(--border, #3a3a3a);
  }
  .panel-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.6rem 0.75rem;
    border-bottom: 1px solid var(--border, #3a3a3a);
    flex-shrink: 0;
  }
  .label {
    font-size: 0.78rem;
    color: var(--text-secondary, #aaa);
    white-space: nowrap;
  }

  /* refresh dropdown */
  .refresh-wrap {
    position: relative;
    display: flex;
    gap: 1px;
    flex-shrink: 0;
  }
  .refresh-btn {
    padding: 0.3rem 0.55rem;
    border-radius: 5px 0 0 5px;
    border: 1px solid var(--border, #555);
    border-right: none;
    background: var(--input-bg, #2a2a2a);
    color: var(--text-secondary, #ccc);
    font-size: 0.8rem;
    cursor: pointer;
    white-space: nowrap;
  }
  .refresh-btn:not(:disabled):hover { background: var(--hover, #3a3a3a); color: var(--text, #f0f0f0); }
  .refresh-arrow {
    padding: 0.3rem 0.35rem;
    border-radius: 0 5px 5px 0;
    border: 1px solid var(--border, #555);
    background: var(--input-bg, #2a2a2a);
    color: var(--text-secondary, #ccc);
    font-size: 0.7rem;
    cursor: pointer;
    line-height: 1;
  }
  .refresh-arrow:not(:disabled):hover { background: var(--hover, #3a3a3a); }
  .refresh-btn:disabled, .refresh-arrow:disabled { opacity: 0.4; cursor: not-allowed; }
  .refresh-menu {
    position: absolute;
    top: calc(100% + 4px);
    right: 0;
    min-width: 120px;
    background: var(--surface-elevated, #2c2c2c);
    border: 1px solid var(--border, #3a3a3a);
    border-radius: 7px;
    list-style: none;
    margin: 0;
    padding: 0.3rem 0;
    box-shadow: 0 4px 16px rgba(0,0,0,0.4);
    z-index: 100;
  }
  .refresh-menu li button {
    width: 100%;
    padding: 0.45rem 0.75rem;
    background: none;
    border: none;
    color: var(--text, #f0f0f0);
    font-size: 0.85rem;
    text-align: left;
    cursor: pointer;
    display: flex;
    align-items: center;
    gap: 0.25rem;
    border-radius: 4px;
  }
  .refresh-menu li button:hover { background: var(--hover, #3a3a3a); }
  .refresh-menu li button.active { color: var(--accent, #4a7ef5); }
  .check { width: 1rem; font-size: 0.8rem; flex-shrink: 0; }

  /* filter button */
  .filter-btn {
    flex-shrink: 0;
    padding: 0.3rem 0.55rem;
    border-radius: 5px;
    border: 1px solid var(--border, #555);
    background: var(--input-bg, #2a2a2a);
    color: var(--text-secondary, #ccc);
    font-size: 0.8rem;
    cursor: pointer;
    white-space: nowrap;
    display: flex;
    align-items: center;
    gap: 0.3rem;
  }
  .filter-btn:hover { background: var(--hover, #3a3a3a); color: var(--text, #f0f0f0); }
  .filter-btn.active { color: var(--accent, #4a7ef5); border-color: var(--accent, #4a7ef5); }
  .filter-btn.open { background: var(--hover, #3a3a3a); }
  .filter-badge {
    background: var(--accent, #4a7ef5);
    color: #fff;
    font-size: 0.68rem;
    font-weight: 700;
    border-radius: 99px;
    padding: 0 0.35rem;
    line-height: 1.4;
  }

  /* M12b — "N new commits" badge */
  .new-commits-badge {
    flex-shrink: 0;
    background: var(--accent, #4a7ef5);
    color: #fff;
    font-size: 0.75rem;
    font-weight: 600;
    border: none;
    border-radius: 99px;
    padding: 0.25rem 0.6rem;
    cursor: pointer;
    white-space: nowrap;
  }
  .new-commits-badge:hover { filter: brightness(1.1); }

  /* filter bar */
  .filter-bar {
    padding: 0.5rem 0.75rem 0.4rem;
    border-bottom: 1px solid var(--accent, #4a7ef5);
    background: rgba(74, 126, 245, 0.05);
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    flex-shrink: 0;
  }
  .filter-row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }
  .filter-label {
    font-size: 0.73rem;
    color: var(--text-muted, #888);
    white-space: nowrap;
    width: 3.5rem;
    flex-shrink: 0;
    text-align: right;
  }
  .filter-label-mid {
    width: auto;
    margin-left: 0.3rem;
  }
  .filter-input {
    flex: 1;
    padding: 0.22rem 0.45rem;
    border-radius: 4px;
    border: 1px solid var(--border, #444);
    background: var(--input-bg, #1e1e1e);
    color: var(--text, #f0f0f0);
    font-size: 0.82rem;
    font-family: ui-monospace, monospace;
    outline: none;
    min-width: 0;
  }
  .filter-input:focus { border-color: var(--accent, #4a7ef5); }
  .filter-date { flex: 0 0 7rem; }
  .filter-actions {
    display: flex;
    gap: 0.4rem;
    justify-content: flex-end;
    padding-top: 0.1rem;
  }
  .filter-apply-btn {
    padding: 0.22rem 0.75rem;
    border-radius: 4px;
    border: none;
    background: var(--accent, #4a7ef5);
    color: #fff;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
  }
  .filter-apply-btn:hover { opacity: 0.85; }
  .filter-clear-btn {
    padding: 0.22rem 0.65rem;
    border-radius: 4px;
    border: 1px solid var(--border, #555);
    background: none;
    color: var(--text-secondary, #aaa);
    font-size: 0.8rem;
    cursor: pointer;
  }
  .filter-clear-btn:not(:disabled):hover { color: #ef5350; border-color: #ef5350; }
  .filter-clear-btn:disabled { opacity: 0.35; cursor: not-allowed; }

  /* presets */
  .preset-wrap { position: relative; margin-right: auto; }
  .preset-toggle-btn {
    padding: 0.22rem 0.55rem;
    border-radius: 4px;
    border: 1px solid var(--border, #555);
    background: none;
    color: var(--text-secondary, #aaa);
    font-size: 0.78rem;
    cursor: pointer;
    white-space: nowrap;
    display: flex;
    align-items: center;
    gap: 0.25rem;
    max-width: 160px;
  }
  .preset-toggle-btn:hover { background: var(--hover, #3a3a3a); color: var(--text, #f0f0f0); }
  .preset-toggle-btn.has-active { color: var(--accent, #4a7ef5); border-color: var(--accent, #4a7ef5); }
  .preset-active-name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 110px;
  }
  .preset-menu {
    position: absolute;
    bottom: calc(100% + 4px);
    left: 0;
    min-width: 180px;
    background: var(--surface-elevated, #2c2c2c);
    border: 1px solid var(--border, #3a3a3a);
    border-radius: 7px;
    list-style: none;
    margin: 0;
    padding: 0.3rem 0;
    box-shadow: 0 4px 16px rgba(0,0,0,0.4);
    z-index: 200;
  }
  .preset-empty { padding: 0.5rem 0.75rem; font-size: 0.8rem; color: var(--text-muted, #666); }
  .preset-item {
    display: flex;
    align-items: center;
    padding: 0.4rem 0.75rem;
    cursor: pointer;
    font-size: 0.83rem;
    color: var(--text, #f0f0f0);
  }
  .preset-item:hover { background: var(--hover, #3a3a3a); }
  .preset-name { flex: 1; }
  .preset-del {
    background: none; border: none; color: var(--text-muted, #666);
    cursor: pointer; font-size: 0.75rem; padding: 0.1rem 0.3rem; border-radius: 3px;
  }
  .preset-del:hover { color: #ef5350; }
  .preset-save-btn {
    padding: 0.22rem 0.6rem;
    border-radius: 4px;
    border: 1px solid var(--border, #555);
    background: none;
    color: var(--text-secondary, #aaa);
    font-size: 0.78rem;
    cursor: pointer;
  }
  .preset-save-btn:not(:disabled):hover { color: var(--accent, #4a7ef5); border-color: var(--accent, #4a7ef5); }
  .preset-save-btn:disabled { opacity: 0.35; cursor: not-allowed; }
  .preset-save-btn.save-overwrite {
    border-radius: 4px 0 0 4px;
    border-right: none;
    color: var(--accent, #4a7ef5);
    border-color: var(--accent, #4a7ef5);
  }
  .preset-save-btn.save-overwrite:hover { background: rgba(74,126,245,0.12); }
  .save-dropdown-wrap {
    position: relative;
    display: flex;
  }
  .save-arrow {
    padding: 0.22rem 0.35rem;
    border-radius: 0 4px 4px 0;
    border: 1px solid var(--accent, #4a7ef5);
    background: none;
    color: var(--accent, #4a7ef5);
    font-size: 0.7rem;
    cursor: pointer;
    line-height: 1;
  }
  .save-arrow:hover { background: rgba(74,126,245,0.12); }
  .save-menu {
    position: absolute;
    bottom: calc(100% + 4px);
    left: 0;
    min-width: 160px;
    background: var(--surface-elevated, #2c2c2c);
    border: 1px solid var(--border, #3a3a3a);
    border-radius: 7px;
    list-style: none;
    margin: 0;
    padding: 0.3rem 0;
    box-shadow: 0 4px 16px rgba(0,0,0,0.4);
    z-index: 200;
  }
  .save-menu li button {
    width: 100%;
    padding: 0.45rem 0.75rem;
    background: none;
    border: none;
    color: var(--text, #f0f0f0);
    font-size: 0.83rem;
    text-align: left;
    cursor: pointer;
    border-radius: 4px;
  }
  .save-menu li button:hover { background: var(--hover, #3a3a3a); }
  .preset-name-input {
    flex: 1;
    padding: 0.2rem 0.45rem;
    border-radius: 4px;
    border: 1px solid var(--accent, #4a7ef5);
    background: var(--input-bg, #1e1e1e);
    color: var(--text, #f0f0f0);
    font-size: 0.82rem;
    outline: none;
    min-width: 0;
  }

  /* commit badges (M6c) */
  .badge {
    display: inline-block;
    font-size: 0.65rem;
    font-weight: 700;
    padding: 0.05rem 0.35rem;
    border-radius: 3px;
    margin-right: 0.3rem;
    vertical-align: middle;
    letter-spacing: 0.02em;
    flex-shrink: 0;
  }
  .badge-feat     { background: rgba(74,222,128,0.18); color: #4ade80; }
  .badge-fix      { background: rgba(248,113,113,0.18); color: #f87171; }
  .badge-docs     { background: rgba(96,165,250,0.18); color: #60a5fa; }
  .badge-test     { background: rgba(167,139,250,0.18); color: #a78bfa; }
  .badge-perf     { background: rgba(251,191,36,0.18); color: #fbbf24; }
  .badge-chore    { background: rgba(156,163,175,0.15); color: #9ca3af; }
  .badge-breaking { background: rgba(239,68,68,0.25); color: #ef4444; }
  .badge-jira     { background: rgba(56,189,248,0.18); color: #38bdf8; }
  .commit-list {
    flex: 1;
    overflow-y: auto;
    padding: 0.25rem 0;
    transition: opacity 0.12s ease;
  }
  .commit-list.is-loading { opacity: 0.5; pointer-events: none; }
  .empty {
    padding: 1.5rem;
    text-align: center;
    font-size: 0.85rem;
    color: var(--text-muted, #666);
  }
  .loading-more {
    padding: 0.6rem;
    text-align: center;
    font-size: 0.78rem;
    color: var(--text-muted, #666);
  }
  .commit-row {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.5rem 0.75rem;
    cursor: pointer;
    border-bottom: 1px solid var(--border-subtle, #2e2e2e);
    transition: background 0.1s;
  }
  .commit-row:hover { background: var(--hover, #2e2e2e); }
  .commit-row.checked { background: var(--selected, #1a2a4a); }
  .commit-row.active { outline: 1px solid var(--accent, #4a7ef5); outline-offset: -1px; }
  .commit-row:focus-visible { outline: 2px solid var(--accent, #4a7ef5); outline-offset: -2px; }
  .commit-row.applied { opacity: 0.55; }
  .commit-row.applied .cb-wrap { pointer-events: none; cursor: default; }
  .applied-badge {
    flex-shrink: 0;
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.04em;
    color: #e05555;
    border: 1px solid #e05555;
    border-radius: 3px;
    padding: 1px 5px;
    margin-right: 6px;
    white-space: nowrap;
  }
  .cb-wrap {
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    padding: 6px 10px 6px 2px;
    margin: -6px -10px -6px -2px;
    cursor: pointer;
    border-radius: 4px;
  }
  .cb-wrap:hover { background: rgba(255,255,255,0.06); }
  .cb-wrap input[type="checkbox"] { cursor: pointer; pointer-events: none; }
  .commit-info {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    overflow: hidden;
  }
  .subject {
    font-size: 0.88rem;
    color: var(--text, #f0f0f0);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .meta {
    display: flex;
    gap: 0.5rem;
    font-size: 0.75rem;
    color: var(--text-muted, #888);
    font-family: ui-monospace, monospace;
  }
  .sha { color: var(--accent, #6e9fff); }
</style>
