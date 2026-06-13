<script lang="ts">
  import type { CommitDetail, Remote } from "./rpc-types";
  import { parseForge, forgeCommitUrl, forgeLabel } from "./forge";

  interface Props {
    detail: CommitDetail | null;
    loading: boolean;
    remotes?: Remote[];
    oncopy?: (label: string) => void;
    onopenurl?: (url: string) => void;
  }

  let { detail, loading, remotes = [], oncopy, onopenurl }: Props = $props();

  // Prefer "origin" if available, else first remote with a recognizable forge,
  // else first remote at all. The result drives the "Open in <forge>" button.
  const forge = $derived.by(() => {
    if (remotes.length === 0) return null;
    const ordered = [...remotes].sort((a, b) => {
      if (a.name === "origin") return -1;
      if (b.name === "origin") return 1;
      return 0;
    });
    for (const r of ordered) {
      const info = parseForge(r.pushUrl || r.fetchUrl);
      if (info && info.kind !== "unknown") return info;
    }
    return parseForge(ordered[0].pushUrl || ordered[0].fetchUrl);
  });

  async function copyText(text: string, label: string) {
    try {
      await navigator.clipboard.writeText(text);
      oncopy?.(label);
    } catch {
      // ignore — clipboard may be unavailable in some Tauri contexts
    }
  }

  function openCommitUrl() {
    if (!detail || !forge) return;
    onopenurl?.(forgeCommitUrl(forge, detail.sha));
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
</script>

<div class="panel">
  <div class="panel-header">Commit detail</div>

  {#if loading}
    <div class="empty">Loading…</div>
  {:else if !detail}
    <div class="empty">Select a commit to view details.</div>
  {:else}
    <div class="body">
      <div class="subject-row">
        <span class="subject">{detail.subject}</span>
        <button
          class="icon-btn"
          onclick={() => copyText(detail.subject, "Subject")}
          title="Copy subject"
          aria-label="Copy subject"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
          </svg>
        </button>
      </div>

      {#if detail.body}
        <pre class="message-body">{detail.body}</pre>
      {/if}

      <div class="meta-grid">
        <span class="meta-key">SHA</span>
        <span class="meta-val mono sha-cell">
          <span class="sha-short">{shortSha(detail.sha)}</span>
          <span class="sha-full">{detail.sha}</span>
          <button
            class="icon-btn"
            onclick={() => copyText(detail.sha, "SHA")}
            title="Copy SHA"
            aria-label="Copy SHA"
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
              <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
            </svg>
          </button>
          {#if forge && forge.kind !== "unknown"}
            <button
              class="icon-btn"
              onclick={openCommitUrl}
              title="Open commit on {forgeLabel(forge.kind)}"
              aria-label="Open commit on {forgeLabel(forge.kind)}"
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>
                <polyline points="15 3 21 3 21 9"/>
                <line x1="10" y1="14" x2="21" y2="3"/>
              </svg>
            </button>
          {/if}
        </span>

        <span class="meta-key">Author</span>
        <span class="meta-val">{detail.author} &lt;{detail.email}&gt;</span>

        <span class="meta-key">Date</span>
        <span class="meta-val mono">{fmt(detail.time)}</span>

        {#if (detail.parents ?? []).length > 0}
          <span class="meta-key">Parent{detail.parents.length > 1 ? "s" : ""}</span>
          <span class="meta-val mono">{detail.parents.map((p) => p.slice(0, 7)).join("  ")}</span>
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  .panel {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
    border-right: 1px solid var(--border, #3a3a3a);
    background: var(--input-bg, #1e1e1e);
  }
  .panel-header {
    padding: 0.35rem 0.75rem;
    font-size: 0.72rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-secondary, #aaa);
    border-bottom: 1px solid var(--border, #3a3a3a);
    flex-shrink: 0;
  }
  .empty {
    padding: 1rem;
    font-size: 0.82rem;
    color: var(--text-muted, #666);
    text-align: center;
  }
  .body {
    flex: 1;
    overflow-y: auto;
    padding: 0.6rem 0.75rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .subject-row {
    display: flex;
    align-items: flex-start;
    gap: 0.4rem;
  }
  .subject {
    flex: 1;
    font-size: 0.9rem;
    font-weight: 600;
    color: var(--text, #f0f0f0);
    line-height: 1.35;
  }
  .icon-btn {
    flex-shrink: 0;
    width: 22px;
    height: 22px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--text-muted, #888);
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    transition: background 0.1s ease, color 0.1s ease;
  }
  .icon-btn:hover { background: var(--hover, #3a3a3a); color: var(--accent, #4a7ef5); }
  .sha-cell {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    overflow: visible;
  }
  .sha-short { flex-shrink: 0; }
  .message-body {
    font-family: ui-monospace, monospace;
    font-size: 0.78rem;
    color: var(--text-secondary, #ccc);
    white-space: pre-wrap;
    word-break: break-word;
    margin: 0;
    padding: 0.4rem 0.5rem;
    background: rgba(255,255,255,0.04);
    border-radius: 4px;
    border-left: 2px solid var(--border, #3a3a3a);
    max-height: 80px;
    overflow-y: auto;
  }
  .meta-grid {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.15rem 0.75rem;
    font-size: 0.78rem;
  }
  .meta-key {
    color: var(--text-muted, #666);
    white-space: nowrap;
  }
  .meta-val {
    color: var(--text-secondary, #ccc);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .mono { font-family: ui-monospace, monospace; }
  .sha-full {
    font-size: 0.7rem;
    color: var(--text-muted, #666);
    margin-left: 0.4rem;
  }
</style>
