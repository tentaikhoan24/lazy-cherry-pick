<script lang="ts">
  import type { Branch } from "./rpc-types";

  interface Props {
    branches: Branch[];
    value: string;
    disabled?: boolean;
    id?: string;
    onchange: (branch: string) => void;
  }

  let { branches, value, disabled = false, id, onchange }: Props = $props();

  let open = $state(false);
  let query = $state("");
  let activeIdx = $state(0);
  let searchInput: HTMLInputElement | undefined = $state();
  let listEl: HTMLUListElement | undefined = $state();

  const filtered = $derived(
    query.trim() === ""
      ? branches
      : branches.filter(b => b.name.toLowerCase().includes(query.trim().toLowerCase()))
  );

  const displayLabel = $derived(() => {
    const b = branches.find(b => b.name === value);
    return b ? b.name + (b.isHead ? " (current)" : "") : value;
  });

  function openDropdown() {
    if (disabled) return;
    query = "";
    activeIdx = Math.max(0, filtered.findIndex(b => b.name === value));
    open = true;
    setTimeout(() => searchInput?.focus(), 0);
  }

  function close() {
    open = false;
    query = "";
  }

  function select(name: string) {
    onchange(name);
    close();
  }

  function onKeydown(e: KeyboardEvent) {
    if (!open) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      activeIdx = Math.min(activeIdx + 1, filtered.length - 1);
      scrollActiveIntoView();
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      activeIdx = Math.max(activeIdx - 1, 0);
      scrollActiveIntoView();
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (filtered[activeIdx]) select(filtered[activeIdx].name);
    } else if (e.key === "Escape") {
      close();
    }
  }

  function onQueryInput() {
    activeIdx = 0;
  }

  function scrollActiveIntoView() {
    setTimeout(() => {
      const el = listEl?.children[activeIdx] as HTMLElement | undefined;
      el?.scrollIntoView({ block: "nearest" });
    }, 0);
  }

  function onClickOutside(e: MouseEvent) {
    const target = e.target as HTMLElement;
    if (!target.closest(".bs-wrap")) close();
  }
</script>

<svelte:window onclick={onClickOutside} />

<div class="bs-wrap" {id}>
  <button
    class="bs-trigger"
    class:open
    {disabled}
    onclick={openDropdown}
    type="button"
    title={value}
  >
    <span class="bs-label">{displayLabel()}</span>
    <span class="bs-arrow">▾</span>
  </button>

  {#if open}
    <div class="bs-dropdown" role="listbox">
      <div class="bs-search-wrap">
        <span class="bs-search-icon">🔍</span>
        <input
          bind:this={searchInput}
          class="bs-search"
          type="text"
          placeholder="Search branch…"
          bind:value={query}
          oninput={onQueryInput}
          onkeydown={onKeydown}
          spellcheck={false}
          autocomplete="off"
        />
      </div>
      <ul bind:this={listEl} class="bs-list">
        {#if filtered.length === 0}
          <li class="bs-empty">No branches match</li>
        {:else}
          {#each filtered as b, i}
            <li
              class="bs-item"
              class:selected={b.name === value}
              class:active={i === activeIdx}
              role="option"
              aria-selected={b.name === value}
              onmouseenter={() => (activeIdx = i)}
              onclick={() => select(b.name)}
            >
              <span class="bs-check">{b.name === value ? "✓" : ""}</span>
              <span class="bs-name" class:remote={b.remote}>{b.name}</span>
              {#if b.isHead}<span class="bs-head">(current)</span>{/if}
              {#if b.remote}<span class="bs-remote">remote</span>{/if}
            </li>
          {/each}
        {/if}
      </ul>
    </div>
  {/if}
</div>

<style>
  .bs-wrap {
    position: relative;
    flex: 1;
    min-width: 0;
  }
  .bs-trigger {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 0.3rem;
    padding: 0.3rem 0.5rem;
    border-radius: 5px;
    border: 1px solid var(--border, #555);
    background: var(--input-bg, #2a2a2a);
    color: var(--text, #f0f0f0);
    font-size: 0.85rem;
    font-family: ui-monospace, monospace;
    cursor: pointer;
    text-align: left;
    min-width: 0;
  }
  .bs-trigger:disabled { opacity: 0.45; cursor: not-allowed; }
  .bs-trigger:not(:disabled):hover { border-color: var(--accent, #4a7ef5); }
  .bs-trigger.open { border-color: var(--accent, #4a7ef5); }
  .bs-label {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .bs-arrow {
    flex-shrink: 0;
    font-size: 0.7rem;
    color: var(--text-muted, #888);
  }

  .bs-dropdown {
    position: absolute;
    top: calc(100% + 4px);
    left: 0;
    right: 0;
    min-width: 220px;
    background: var(--surface-elevated, #2c2c2c);
    border: 1px solid var(--accent, #4a7ef5);
    border-radius: 7px;
    box-shadow: 0 6px 20px rgba(0,0,0,0.5);
    z-index: 200;
    overflow: hidden;
  }
  .bs-search-wrap {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.4rem 0.6rem;
    border-bottom: 1px solid var(--border, #3a3a3a);
  }
  .bs-search-icon { font-size: 0.8rem; flex-shrink: 0; }
  .bs-search {
    flex: 1;
    background: none;
    border: none;
    outline: none;
    color: var(--text, #f0f0f0);
    font-size: 0.85rem;
    font-family: ui-monospace, monospace;
  }
  .bs-search::placeholder { color: var(--text-muted, #666); }

  .bs-list {
    list-style: none;
    margin: 0;
    padding: 0.3rem 0;
    max-height: 260px;
    overflow-y: auto;
  }
  .bs-empty {
    padding: 0.6rem 1rem;
    font-size: 0.82rem;
    color: var(--text-muted, #666);
  }
  .bs-item {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    padding: 0.4rem 0.6rem;
    cursor: pointer;
    font-size: 0.85rem;
    font-family: ui-monospace, monospace;
  }
  .bs-item.active { background: var(--hover, #3a3a3a); }
  .bs-item.selected { color: var(--accent, #4a7ef5); }
  .bs-check { width: 1rem; flex-shrink: 0; font-size: 0.8rem; }
  .bs-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .bs-name.remote { color: var(--text-secondary, #aaa); font-style: italic; }
  .bs-head { flex-shrink: 0; font-size: 0.72rem; color: var(--text-muted, #888); }
  .bs-remote {
    flex-shrink: 0;
    font-size: 0.65rem;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: #6da4dd;
    border: 1px solid #6da4dd;
    border-radius: 3px;
    padding: 1px 5px;
  }
</style>
