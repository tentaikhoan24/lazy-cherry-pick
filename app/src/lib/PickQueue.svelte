<script lang="ts">
  import type { Branch, Commit, CherryPickProgress, DryRunItem, Remote, PRSummary } from "./rpc-types";
  import BranchSelect from "./BranchSelect.svelte";

  interface Props {
    queue: Commit[];
    branches: Branch[];
    remotes: Remote[];
    targetBranch: string;
    sourceBranch: string;
    busy: boolean;
    progress: CherryPickProgress | null;
    dryRunMap: Map<string, DryRunItem>;
    defaultApplyMode?: "apply" | "apply-push" | "apply-push-pr";
    defaultPushRemote?: string;
    /** True when the current repo has a forge connection saved — enables the
     * "Apply & Push & Create PR" option in the dropdown. */
    forgeConnected?: boolean;
    /** Open PRs for the current target branch — surfaces existing PRs so the
     * user doesn't blindly try to create one that already exists. */
    targetPRs?: PRSummary[];
    prCheckLoading?: boolean;
    onopenpr?: (url: string) => void;
    onrefreshprs?: () => void;
    /** Open the CreatePR dialog directly — used when the user already has
     * commits on the branch (pushed elsewhere) and just wants to file a PR. */
    oncreatepr?: () => void;
    ontargetbranch: (branch: string) => void;
    onremove: (sha: string) => void;
    onreorder: (from: number, to: number) => void;
    onapply: () => void;
    onapplypush: (remote: string) => void;
    onapplypushpr: (remote: string) => void;
    oncancel: () => void;
    oncreate: (name: string, base: string) => void;
  }

  let { queue, branches, remotes, targetBranch, sourceBranch, busy, progress, dryRunMap, defaultApplyMode = "apply", defaultPushRemote = "origin", forgeConnected = false, targetPRs = [], prCheckLoading = false, onopenpr, onrefreshprs, oncreatepr, ontargetbranch, onremove, onreorder, onapply, onapplypush, onapplypushpr, oncancel, oncreate }: Props = $props();

  // Target must be a LOCAL branch — cherry-picking onto a remote-tracking ref
  // (origin/main) puts the repo in detached HEAD. Source dropdown can still
  // show remote branches; only the target is restricted here.
  const localBranches = $derived(branches.filter((b) => !b.remote));

  // If targetBranch state ended up holding a remote name (e.g. from a previous
  // app version that didn't filter, or because the repo opened in detached
  // HEAD with branch=""), auto-switch to a sane local default.
  $effect(() => {
    if (localBranches.length === 0) return;
    const isValid = localBranches.some((b) => b.name === targetBranch);
    if (!isValid) {
      const head = localBranches.find((b) => b.isHead);
      const fallback = head ?? localBranches[0];
      if (fallback.name !== targetBranch) ontargetbranch(fallback.name);
    }
  });

  // ── keyboard + drag-drop ───────────────────────────────────
  let queueRowEls: (HTMLElement | undefined)[] = [];
  let dragFromIdx = $state<number | null>(null);
  let dragOverIdx = $state<number | null>(null);

  const canApply = $derived(queue.length > 0 && targetBranch !== sourceBranch && !busy);

  let dropdownOpen = $state(false);
  let mode = $state<"apply" | "apply-push" | "apply-push-pr">(defaultApplyMode);
  let pushRemote = $state(defaultPushRemote);

  // Re-sync pushRemote if the list changes and the current choice is gone
  // (e.g. user removed a remote, or opened a different repo).
  $effect(() => {
    if (remotes.length === 0) return;
    if (!remotes.some((r) => r.name === pushRemote)) {
      pushRemote = remotes.find((r) => r.name === "origin")?.name ?? remotes[0].name;
    }
  });

  const btnLabel = $derived(
    mode === "apply-push-pr"
      ? `Apply & Push & PR ${queue.length > 0 ? queue.length : ""} commit${queue.length === 1 ? "" : "s"} → ${pushRemote}/${targetBranch}`
      : mode === "apply-push"
        ? `Apply & Push ${queue.length > 0 ? queue.length : ""} commit${queue.length === 1 ? "" : "s"} → ${pushRemote}/${targetBranch}`
        : `Apply ${queue.length > 0 ? queue.length : ""} commit${queue.length === 1 ? "" : "s"} → ${targetBranch}`
  );

  // ── create branch inline form ─────────────────────────────
  let creatingBranch = $state(false);
  let newBranchName = $state("");
  let newBranchInput: HTMLInputElement | undefined = $state();

  function openCreateForm() {
    newBranchName = "";
    creatingBranch = true;
    // focus after DOM update
    setTimeout(() => newBranchInput?.focus(), 0);
  }

  function cancelCreate() {
    creatingBranch = false;
    newBranchName = "";
  }

  function confirmCreate() {
    const name = newBranchName.trim();
    if (!name) return;
    oncreate(name, targetBranch);
    creatingBranch = false;
    newBranchName = "";
  }

  function onCreateKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") confirmCreate();
    else if (e.key === "Escape") cancelCreate();
  }

  function shortSha(sha: string) { return sha.slice(0, 7); }

  function selectMode(m: "apply" | "apply-push" | "apply-push-pr", remote?: string) {
    mode = m;
    if (remote) pushRemote = remote;
    dropdownOpen = false;
  }

  function execute() {
    if (mode === "apply-push-pr") onapplypushpr(pushRemote);
    else if (mode === "apply-push") onapplypush(pushRemote);
    else onapply();
  }

  function toggleDropdown() {
    if (canApply) dropdownOpen = !dropdownOpen;
  }

  function onClickOutside(e: MouseEvent) {
    const target = e.target as HTMLElement;
    if (!target.closest(".dropdown-wrap")) dropdownOpen = false;
  }

  // ── M14b — PR popover (rich hover details) ────────────────
  /** Number of the PR whose popover is currently visible, or null. */
  let hoveredPrNumber = $state<number | null>(null);
  let hoverTimer: ReturnType<typeof setTimeout> | null = null;

  function showPrPopover(number: number) {
    if (hoverTimer) { clearTimeout(hoverTimer); hoverTimer = null; }
    hoveredPrNumber = number;
  }
  function hidePrPopover() {
    // Small delay so the user can move the cursor INTO the popover (e.g. to
    // click the description text). Cancelled when entering the popover.
    hoverTimer = setTimeout(() => { hoveredPrNumber = null; }, 150);
  }
  function keepPrPopover() {
    if (hoverTimer) { clearTimeout(hoverTimer); hoverTimer = null; }
  }

  /** "2h ago" / "yesterday" / "Mar 5" — for the popover. Empty input → "". */
  function relTime(iso: string): string {
    if (!iso) return "";
    const t = Date.parse(iso);
    if (Number.isNaN(t)) return "";
    const diffMs = Date.now() - t;
    const sec = Math.floor(diffMs / 1000);
    if (sec < 60) return "just now";
    const min = Math.floor(sec / 60);
    if (min < 60) return `${min}m ago`;
    const hr = Math.floor(min / 60);
    if (hr < 24) return `${hr}h ago`;
    const day = Math.floor(hr / 24);
    if (day === 1) return "yesterday";
    if (day < 7) return `${day}d ago`;
    if (day < 30) return `${Math.floor(day / 7)}w ago`;
    return new Date(t).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
  }
</script>

<svelte:window onclick={onClickOutside} />

<div class="panel">
  <div class="panel-header">
    <label class="label">Target branch</label>
    <BranchSelect
      branches={localBranches}
      value={targetBranch}
      disabled={localBranches.length === 0 || busy || creatingBranch}
      onchange={ontargetbranch}
    />
    <button
      class="new-branch-btn"
      onclick={openCreateForm}
      disabled={busy || creatingBranch}
      title="Create new branch from {targetBranch}"
    >+</button>
  </div>

  {#if creatingBranch}
    <div class="create-branch-form">
      <input
        bind:this={newBranchInput}
        class="branch-name-input"
        type="text"
        placeholder="new-branch-name"
        bind:value={newBranchName}
        onkeydown={onCreateKeydown}
        spellcheck={false}
      />
      <span class="from-label">from {targetBranch}</span>
      <button class="create-confirm-btn" onclick={confirmCreate} disabled={!newBranchName.trim()}>
        Create
      </button>
      <button class="create-cancel-btn" onclick={cancelCreate}>×</button>
    </div>
  {/if}

  <!-- M14b — Open-PR status for the target branch -->
  {#if forgeConnected && targetBranch}
    <div class="pr-status" class:has-pr={targetPRs.length > 0}>
      {#if prCheckLoading}
        <span class="pr-status-text muted">Checking PRs…</span>
      {:else if targetPRs.length === 0}
        <span class="pr-status-text muted">No open PR for <span class="mono">{targetBranch}</span></span>
      {:else}
        <span class="pr-icon">●</span>
        <span class="pr-status-text">
          {targetPRs.length === 1 ? "Open PR:" : `${targetPRs.length} open PRs:`}
        </span>
        <div class="pr-links">
          {#each targetPRs as pr}
            <div
              class="pr-wrap"
              onmouseenter={() => showPrPopover(pr.number)}
              onmouseleave={hidePrPopover}
              role="group"
            >
              <button
                type="button"
                class="pr-link"
                onclick={() => onopenpr?.(pr.url)}
              >
                #{pr.number}{pr.draft ? " (draft)" : ""}
              </button>

              {#if hoveredPrNumber === pr.number}
                <div
                  class="pr-popover"
                  role="tooltip"
                  onmouseenter={keepPrPopover}
                  onmouseleave={hidePrPopover}
                >
                  <div class="pop-title">
                    {#if pr.draft}<span class="pop-draft">DRAFT</span>{/if}
                    <span class="pop-title-text">{pr.title || "(no title)"}</span>
                  </div>

                  <div class="pop-meta">
                    <span class="pop-key">→</span>
                    <span class="pop-val mono">{pr.base || "(unknown)"}</span>
                  </div>

                  {#if pr.author}
                    <div class="pop-meta">
                      <span class="pop-key">by</span>
                      <span class="pop-val">{pr.author}</span>
                    </div>
                  {/if}

                  {#if pr.updatedAt}
                    <div class="pop-meta">
                      <span class="pop-key">updated</span>
                      <span class="pop-val" title={pr.updatedAt}>{relTime(pr.updatedAt)}</span>
                    </div>
                  {/if}

                  {#if pr.createdAt && pr.createdAt !== pr.updatedAt}
                    <div class="pop-meta">
                      <span class="pop-key">opened</span>
                      <span class="pop-val" title={pr.createdAt}>{relTime(pr.createdAt)}</span>
                    </div>
                  {/if}

                  {#if pr.bodyPreview}
                    <div class="pop-body">{pr.bodyPreview}</div>
                  {/if}

                  <button
                    type="button"
                    class="pop-action"
                    onclick={() => onopenpr?.(pr.url)}
                  >
                    Open #{pr.number} in browser ↗
                  </button>
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
      <button
        type="button"
        class="pr-refresh"
        title="Refresh PR status"
        disabled={prCheckLoading}
        onclick={() => onrefreshprs?.()}
      >
        ↻
      </button>
      <button
        type="button"
        class="pr-create"
        title="Create new PR for {targetBranch} (without cherry-picking)"
        onclick={() => oncreatepr?.()}
      >
        +
      </button>
    </div>
  {/if}

  <div class="queue-body">
    {#if queue.length === 0}
      <div class="empty">
        <p>No commits selected.</p>
        <p class="hint">Tick commits on the left to add them here.</p>
      </div>
    {:else}
      <div class="queue-header">
        Pick queue — {queue.length} commit{queue.length === 1 ? "" : "s"}
      </div>
      <ul class="queue-list">
        {#each queue as c, i}
          {@const dryRun = dryRunMap.get(c.sha)}
          <li
            class="queue-item"
            class:conflict={dryRun?.willConflict}
            class:drag-over={dragOverIdx === i}
            draggable="true"
            tabindex="0"
            bind:this={queueRowEls[i]}
            ondragstart={() => (dragFromIdx = i)}
            ondragover={(e) => { e.preventDefault(); dragOverIdx = i; }}
            ondragleave={() => { if (dragOverIdx === i) dragOverIdx = null; }}
            ondrop={() => { if (dragFromIdx !== null && dragFromIdx !== i) onreorder(dragFromIdx, i); dragFromIdx = null; dragOverIdx = null; }}
            ondragend={() => { dragFromIdx = null; dragOverIdx = null; }}
            onkeydown={(e) => {
              if (e.key === 'ArrowDown') { e.preventDefault(); queueRowEls[i + 1]?.focus(); }
              else if (e.key === 'ArrowUp') { e.preventDefault(); i > 0 && queueRowEls[i - 1]?.focus(); }
              else if ((e.key === 'Delete' || e.key === 'Backspace') && !busy) { e.preventDefault(); onremove(c.sha); }
            }}
          >
            <span class="order">{i + 1}</span>
            <div class="commit-info">
              <span class="subject" title={c.subject}>{c.subject}</span>
              <span class="sha">{shortSha(c.sha)}</span>
            </div>
            {#if dryRun?.willConflict}
              <span class="conflict-icon" title="Predicted conflict: {dryRun.files.length > 0 ? dryRun.files.join(', ') : 'unknown files'}">⚠</span>
            {/if}
            <button class="remove-btn" onclick={() => onremove(c.sha)} title="Remove" disabled={busy}>✕</button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  <div class="panel-footer">
    {#if targetBranch === sourceBranch && branches.length > 0}
      <p class="warn">Source and target branch are the same.</p>
    {/if}

    {#if busy}
      <!-- Progress row -->
      <div class="progress-row">
        <div class="progress-bar-wrap">
          <div
            class="progress-bar-fill"
            style="width: {progress ? Math.round((progress.n / progress.total) * 100) : 0}%"
          ></div>
        </div>
        <span class="progress-label">
          {#if progress}
            {progress.n}/{progress.total} — {shortSha(progress.sha)}
          {:else}
            Preparing…
          {/if}
        </span>
        <button class="cancel-btn" onclick={oncancel}>Cancel</button>
      </div>
    {:else}
      <div class="dropdown-wrap">
        <!-- Main button -->
        <button class="apply-btn" onclick={execute} disabled={!canApply}>
          {btnLabel}
        </button>
        <!-- Arrow toggle -->
        <button class="arrow-btn" onclick={toggleDropdown} disabled={!canApply} aria-label="More options">
          ▾
        </button>
        <!-- Dropdown menu -->
        {#if dropdownOpen}
          <ul class="dropdown-menu">
            <li>
              <button class:active={mode === "apply"} onclick={() => selectMode("apply")}>
                {#if mode === "apply"}<span class="check">✓</span>{:else}<span class="check"></span>{/if}
                Apply
              </button>
            </li>
            {#if remotes.length === 0}
              <li>
                <button class:active={mode === "apply-push"} onclick={() => selectMode("apply-push")}>
                  {#if mode === "apply-push"}<span class="check">✓</span>{:else}<span class="check"></span>{/if}
                  Apply &amp; Push
                </button>
              </li>
            {:else}
              {#each remotes as r}
                {@const isActive = mode === "apply-push" && pushRemote === r.name}
                <li>
                  <button class:active={isActive} onclick={() => selectMode("apply-push", r.name)}>
                    {#if isActive}<span class="check">✓</span>{:else}<span class="check"></span>{/if}
                    Apply &amp; Push to <span class="remote-name">{r.name}</span>
                  </button>
                </li>
              {/each}
              {#if forgeConnected}
                <li class="divider" aria-hidden="true"></li>
                {#each remotes as r}
                  {@const isActive = mode === "apply-push-pr" && pushRemote === r.name}
                  <li>
                    <button class:active={isActive} onclick={() => selectMode("apply-push-pr", r.name)}>
                      {#if isActive}<span class="check">✓</span>{:else}<span class="check"></span>{/if}
                      Apply &amp; Push &amp; Create PR to <span class="remote-name">{r.name}</span>
                    </button>
                  </li>
                {/each}
              {/if}
            {/if}
          </ul>
        {/if}
      </div>
    {/if}
  </div>
</div>

<style>
  .panel {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
  }
  .panel-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.6rem 0.75rem;
    border-bottom: 1px solid var(--border, #3a3a3a);
    flex-shrink: 0;
  }
  .new-branch-btn {
    flex-shrink: 0;
    width: 1.7rem;
    height: 1.7rem;
    border-radius: 5px;
    border: 1px solid var(--border, #555);
    background: var(--input-bg, #2a2a2a);
    color: var(--text-secondary, #ccc);
    font-size: 1rem;
    line-height: 1;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .new-branch-btn:not(:disabled):hover { background: var(--hover, #3a3a3a); color: var(--accent, #4a7ef5); }
  .new-branch-btn:disabled { opacity: 0.35; cursor: not-allowed; }

  /* inline create-branch form */
  .create-branch-form {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.45rem 0.75rem;
    background: var(--hover, #272727);
    border-bottom: 1px solid var(--accent, #4a7ef5);
    flex-shrink: 0;
  }
  .branch-name-input {
    flex: 1;
    padding: 0.25rem 0.5rem;
    border-radius: 4px;
    border: 1px solid var(--accent, #4a7ef5);
    background: var(--input-bg, #1e1e1e);
    color: var(--text, #f0f0f0);
    font-size: 0.85rem;
    font-family: ui-monospace, monospace;
    outline: none;
    min-width: 0;
  }
  .from-label {
    flex-shrink: 0;
    font-size: 0.73rem;
    font-family: ui-monospace, monospace;
    color: var(--text-muted, #666);
    white-space: nowrap;
  }
  .create-confirm-btn {
    flex-shrink: 0;
    padding: 0.25rem 0.65rem;
    border-radius: 4px;
    border: none;
    background: var(--accent, #4a7ef5);
    color: #fff;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
  }
  .create-confirm-btn:disabled { opacity: 0.4; cursor: not-allowed; }
  .create-confirm-btn:not(:disabled):hover { opacity: 0.85; }
  .create-cancel-btn {
    flex-shrink: 0;
    background: none;
    border: none;
    color: var(--text-muted, #666);
    font-size: 1rem;
    cursor: pointer;
    padding: 0.1rem 0.3rem;
    line-height: 1;
    border-radius: 3px;
  }
  .create-cancel-btn:hover { color: #ef5350; }

  .label {
    font-size: 0.78rem;
    color: var(--text-secondary, #aaa);
    white-space: nowrap;
  }
  .queue-body {
    flex: 1;
    overflow-y: auto;
    padding: 0.25rem 0;
  }
  .empty {
    padding: 1.5rem;
    text-align: center;
  }
  .empty p { margin: 0.25rem 0; font-size: 0.9rem; color: var(--text-muted, #666); }
  .empty .hint { font-size: 0.8rem; }
  .queue-header {
    padding: 0.4rem 0.75rem;
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--text-secondary, #aaa);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    border-bottom: 1px solid var(--border-subtle, #2e2e2e);
  }
  .queue-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .queue-item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.45rem 0.75rem;
    border-bottom: 1px solid var(--border-subtle, #2e2e2e);
  }
  .order {
    flex-shrink: 0;
    width: 1.5rem;
    text-align: right;
    font-size: 0.75rem;
    color: var(--text-muted, #666);
    font-family: ui-monospace, monospace;
  }
  .commit-info {
    flex: 1;
    overflow: hidden;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }
  .subject {
    font-size: 0.85rem;
    color: var(--text, #f0f0f0);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sha {
    font-size: 0.73rem;
    font-family: ui-monospace, monospace;
    color: var(--accent, #6e9fff);
  }
  .queue-item:focus-visible { outline: 2px solid var(--accent, #4a7ef5); outline-offset: -2px; }
  .queue-item[draggable="true"] { cursor: grab; }
  .queue-item[draggable="true"]:active { cursor: grabbing; }
  .queue-item.drag-over { border-top: 2px dashed var(--accent, #4a7ef5); background: rgba(74, 126, 245, 0.07); }
  .queue-item.conflict {
    background: rgba(255, 152, 0, 0.06);
    border-left: 2px solid #ffa726;
  }
  .conflict-icon {
    flex-shrink: 0;
    font-size: 0.82rem;
    color: #ffa726;
    cursor: default;
  }
  .remove-btn {
    flex-shrink: 0;
    background: none;
    border: none;
    color: var(--text-muted, #666);
    cursor: pointer;
    font-size: 0.8rem;
    padding: 0.2rem 0.3rem;
    border-radius: 4px;
    line-height: 1;
  }
  .remove-btn:hover:not(:disabled) { color: #ef5350; background: rgba(239,83,80,0.1); }
  .remove-btn:disabled { cursor: default; opacity: 0.3; }

  /* footer */
  .panel-footer {
    padding: 0.75rem;
    border-top: 1px solid var(--border, #3a3a3a);
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .warn {
    margin: 0;
    font-size: 0.78rem;
    color: #f4a261;
    text-align: center;
  }

  /* progress */
  .progress-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .progress-bar-wrap {
    flex: 1;
    height: 6px;
    border-radius: 3px;
    background: rgba(255,255,255,0.1);
    overflow: hidden;
  }
  .progress-bar-fill {
    height: 100%;
    border-radius: 3px;
    background: var(--accent, #4a7ef5);
    transition: width 0.2s ease;
  }
  .progress-label {
    flex-shrink: 0;
    font-size: 0.75rem;
    font-family: ui-monospace, monospace;
    color: var(--text-secondary, #aaa);
    white-space: nowrap;
  }
  .cancel-btn {
    flex-shrink: 0;
    padding: 0.3rem 0.65rem;
    border-radius: 5px;
    border: 1px solid #ef5350;
    background: transparent;
    color: #ef5350;
    font-size: 0.8rem;
    cursor: pointer;
  }
  .cancel-btn:hover { background: rgba(239,83,80,0.15); }

  /* dropdown button */
  .dropdown-wrap {
    position: relative;
    display: flex;
    gap: 2px;
  }
  .apply-btn {
    flex: 1;
    padding: 0.6rem 0.75rem;
    border-radius: 7px 0 0 7px;
    border: none;
    background: var(--accent, #396cd8);
    color: #fff;
    font-size: 0.88rem;
    font-weight: 600;
    cursor: pointer;
    transition: opacity 0.15s;
    text-align: left;
  }
  .arrow-btn {
    flex-shrink: 0;
    padding: 0.6rem 0.7rem;
    border-radius: 0 7px 7px 0;
    border: none;
    border-left: 1px solid rgba(255,255,255,0.2);
    background: var(--accent, #396cd8);
    color: #fff;
    font-size: 0.8rem;
    cursor: pointer;
    transition: opacity 0.15s;
    line-height: 1;
  }
  .apply-btn:disabled,
  .arrow-btn:disabled { opacity: 0.35; cursor: not-allowed; }
  .apply-btn:not(:disabled):hover,
  .arrow-btn:not(:disabled):hover { opacity: 0.85; }

  .dropdown-menu {
    position: absolute;
    bottom: calc(100% + 4px);
    right: 0;
    min-width: 160px;
    background: var(--surface-elevated, #2c2c2c);
    border: 1px solid var(--border, #3a3a3a);
    border-radius: 7px;
    list-style: none;
    margin: 0;
    padding: 0.3rem 0;
    box-shadow: 0 4px 16px rgba(0,0,0,0.4);
    z-index: 100;
  }
  .dropdown-menu li button {
    width: 100%;
    padding: 0.5rem 1rem;
    background: none;
    border: none;
    color: var(--text, #f0f0f0);
    font-size: 0.88rem;
    text-align: left;
    cursor: pointer;
    border-radius: 4px;
  }
  .dropdown-menu li button:hover {
    background: var(--hover, #3a3a3a);
  }
  .dropdown-menu li button.active {
    color: var(--accent, #4a7ef5);
  }
  .check {
    display: inline-block;
    width: 1rem;
    font-size: 0.8rem;
  }
  .remote-name {
    font-family: ui-monospace, monospace;
    color: var(--text-secondary, #ccc);
    margin-left: 0.15rem;
  }
  .dropdown-menu li button.active .remote-name { color: var(--accent, #4a7ef5); }
  .dropdown-menu li.divider {
    height: 1px;
    background: var(--border, #3a3a3a);
    margin: 0.25rem 0;
    padding: 0;
    pointer-events: none;
  }

  /* M14b — PR status bar */
  .pr-status {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.4rem 0.75rem;
    border-bottom: 1px solid var(--border, #3a3a3a);
    background: rgba(255, 255, 255, 0.02);
    font-size: 0.78rem;
    flex-shrink: 0;
  }
  .pr-status.has-pr {
    background: rgba(74, 126, 245, 0.08);
    border-bottom-color: rgba(74, 126, 245, 0.3);
  }
  .pr-status-text {
    color: var(--text-secondary, #ccc);
    flex-shrink: 0;
  }
  .pr-status-text.muted { color: var(--text-muted, #888); font-style: italic; }
  .pr-status-text .mono {
    font-family: ui-monospace, monospace;
    color: var(--text, #f0f0f0);
    font-style: normal;
  }
  .pr-icon {
    color: #4caf50;
    font-size: 0.7rem;
    flex-shrink: 0;
  }
  .pr-links {
    display: flex;
    gap: 0.35rem;
    flex: 1;
    flex-wrap: wrap;
    min-width: 0;
  }
  .pr-wrap {
    position: relative;
    display: inline-flex;
  }
  .pr-link {
    background: none;
    border: 1px solid var(--accent, #4a7ef5);
    border-radius: 3px;
    padding: 1px 6px;
    color: var(--accent, #4a7ef5);
    font-size: 0.75rem;
    font-family: ui-monospace, monospace;
    font-weight: 600;
    cursor: pointer;
    text-decoration: none;
  }
  .pr-link:hover { background: var(--accent, #4a7ef5); color: white; }

  /* hover popover */
  .pr-popover {
    position: absolute;
    top: calc(100% + 6px);
    left: 0;
    z-index: 100;
    min-width: 240px;
    max-width: 360px;
    background: var(--surface-elevated, #2c2c2c);
    border: 1px solid var(--accent, #4a7ef5);
    border-radius: 6px;
    box-shadow: 0 6px 24px rgba(0, 0, 0, 0.5);
    padding: 0.55rem 0.65rem;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    font-style: normal;
    cursor: default;
  }
  .pop-title {
    display: flex;
    align-items: flex-start;
    gap: 0.35rem;
    font-size: 0.83rem;
    font-weight: 600;
    color: var(--text, #f0f0f0);
    line-height: 1.3;
  }
  .pop-draft {
    flex-shrink: 0;
    font-size: 0.62rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    padding: 1px 5px;
    border-radius: 3px;
    background: rgba(255, 200, 80, 0.15);
    color: #ffc850;
    border: 1px solid #ffc850;
    line-height: 1.2;
  }
  .pop-title-text {
    word-break: break-word;
    overflow-wrap: break-word;
  }
  .pop-meta {
    display: flex;
    gap: 0.5rem;
    font-size: 0.74rem;
    line-height: 1.3;
  }
  .pop-key {
    flex-shrink: 0;
    color: var(--text-muted, #888);
    min-width: 50px;
  }
  .pop-val {
    color: var(--text-secondary, #ccc);
    word-break: break-word;
  }
  .pop-val.mono { font-family: ui-monospace, monospace; }
  .pop-body {
    margin-top: 0.2rem;
    padding-top: 0.4rem;
    border-top: 1px solid var(--border, #3a3a3a);
    font-size: 0.74rem;
    color: var(--text-secondary, #aaa);
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.4;
    max-height: 100px;
    overflow-y: auto;
  }
  .pop-action {
    margin-top: 0.3rem;
    padding: 0.3rem 0.6rem;
    background: var(--accent, #4a7ef5);
    border: none;
    border-radius: 4px;
    color: white;
    font-size: 0.78rem;
    font-weight: 600;
    cursor: pointer;
    text-align: center;
  }
  .pop-action:hover { filter: brightness(1.1); }
  .pr-refresh, .pr-create {
    background: none;
    border: none;
    color: var(--text-muted, #888);
    cursor: pointer;
    font-size: 0.95rem;
    padding: 0 0.25rem;
    flex-shrink: 0;
    line-height: 1;
  }
  .pr-refresh:hover:not(:disabled), .pr-create:hover { color: var(--accent, #4a7ef5); }
  .pr-refresh:disabled { opacity: 0.4; cursor: not-allowed; }
  .pr-create { font-weight: 700; font-size: 1.1rem; }
</style>
