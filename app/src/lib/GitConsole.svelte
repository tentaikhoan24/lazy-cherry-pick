<script lang="ts">
  import { invoke } from "@tauri-apps/api/core";
  import { listen } from "@tauri-apps/api/event";
  import { onMount } from "svelte";

  interface Props {
    height?: number;
    onclose: () => void;
  }

  let { height = 180, onclose }: Props = $props();

  interface LogEntry {
    ts: number;
    type: "cmd" | "info" | "ai";
    cmd: string;
    branch?: string | null;
    ms?: number | null;
    // "ai" entries only — one headless AI CLI invocation (M16/M16b)
    command?: string;
    args?: string[];
    promptVia?: string;
    promptChars?: number;
    success?: boolean;
    costUsd?: number;
    durationMs?: number;
    error?: string;
  }

  let entries = $state<LogEntry[]>([]);
  let bodyEl = $state<HTMLDivElement | null>(null);
  let autoScroll = $state(true);

  function formatTime(ts: number): string {
    return new Date(ts * 1000).toLocaleTimeString("en", {
      hour12: false, hour: "2-digit", minute: "2-digit", second: "2-digit",
    });
  }

  function scrollToBottom() {
    if (bodyEl && autoScroll) bodyEl.scrollTop = bodyEl.scrollHeight;
  }

  // Subcommand is the first word after "git " — the interesting part to highlight.
  function splitGit(cmd: string): { sub: string; rest: string } {
    const s = cmd.startsWith("git ") ? cmd.slice(4) : cmd;
    const sp = s.indexOf(" ");
    return sp < 0 ? { sub: s, rest: "" } : { sub: s.slice(0, sp), rest: s.slice(sp) };
  }

  // Pretty-print method name for info headers: git.cherryPick → cherry-pick
  function fmtMethod(label: string): string {
    const arrow = label.indexOf(" → ");
    const bracket = label.indexOf(" [");
    const sep = arrow >= 0 ? arrow : bracket >= 0 ? bracket : -1;
    const method = sep >= 0 ? label.slice(0, sep) : label;
    const suffix = sep >= 0 ? label.slice(sep) : "";
    const name = method.replace(/^git\./, "").replace(/([A-Z])/g, " $1").toLowerCase().trim();
    return name + suffix;
  }

  onMount(() => {
    invoke<LogEntry[]>("git_log_read").then(data => {
      entries = data;
      setTimeout(scrollToBottom, 0);
    }).catch(() => {});

    let unlisten: (() => void) | null = null;
    listen<LogEntry>("git-log", (e) => {
      entries = [...entries, e.payload];
      setTimeout(scrollToBottom, 0);
    }).then(fn => { unlisten = fn; });

    return () => { unlisten?.(); };
  });

  async function clear() {
    try {
      await invoke("git_log_clear");
      entries = [];
    } catch {}
  }

  function onScroll() {
    if (!bodyEl) return;
    const atBottom = bodyEl.scrollHeight - bodyEl.scrollTop - bodyEl.clientHeight < 8;
    autoScroll = atBottom;
  }
</script>

<div class="console" style="height: {height}px">
  <div class="console-header">
    <svg class="console-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
      <polyline points="4 17 10 11 4 5"/>
      <line x1="12" y1="19" x2="20" y2="19"/>
    </svg>
    <span class="console-title">Git Console</span>
    <span class="entry-count">{entries.length} entr{entries.length === 1 ? "y" : "ies"}</span>
    <div class="console-actions">
      <button class="hdr-btn" onclick={clear} title="Clear log">Clear</button>
      <button class="hdr-btn close" onclick={onclose} title="Close console">✕</button>
    </div>
  </div>

  <div class="console-body" bind:this={bodyEl} onscroll={onScroll}>
    {#if entries.length === 0}
      <span class="empty-hint">No git commands logged yet. Open a repo or apply commits to see commands here.</span>
    {/if}
    {#each entries as e}
      {#if e.type === "info"}
        <div class="log-info">
          <span class="log-info-label">{fmtMethod(e.cmd)}</span>
          <span class="log-info-ts">{formatTime(e.ts)}</span>
        </div>
      {:else if e.type === "ai"}
        <div class="log-ai" class:log-ai-error={!e.success}>
          <span class="log-ts">{formatTime(e.ts)}</span>
          <span class="ai-icon">🤖</span>
          <span class="ai-cmd" title={[e.command, ...(e.args ?? [])].join(" ")}>
            <span class="ai-name">{e.command}</span><span class="ai-args">{" "}{(e.args ?? []).join(" ")}</span>
          </span>
          <span class="ai-prompt">prompt {e.promptChars}c via {e.promptVia}</span>
          {#if e.success}
            <span class="ai-status ok">✓</span>
            {#if (e.costUsd ?? 0) > 0}
              <span class="ai-cost">${e.costUsd?.toFixed(4)}</span>
            {/if}
          {:else}
            <span class="ai-status err" title={e.error}>✗</span>
          {/if}
          {#if e.durationMs != null}
            <span class="log-ms">{e.durationMs}ms</span>
          {/if}
        </div>
        {#if !e.success && e.error}
          <div class="log-ai-error-detail">{e.error}</div>
        {/if}
      {:else}
        {@const { sub, rest } = splitGit(e.cmd)}
        <div class="log-line">
          <span class="log-ts">{formatTime(e.ts)}</span>
          <span class="log-cmd" title={e.cmd}>
            <span class="log-git">git{" "}</span><span class="log-sub">{sub}</span><span class="log-rest">{rest}</span>
          </span>
          {#if e.branch}
            <span class="log-branch">{e.branch}</span>
          {/if}
          {#if e.ms != null}
            <span class="log-ms">{e.ms}ms</span>
          {/if}
        </div>
      {/if}
    {/each}
  </div>
</div>

<style>
  .console {
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    background: #111;
    font-family: ui-monospace, "Cascadia Code", Consolas, monospace;
  }

  .console-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.3rem 0.75rem;
    background: #1a1a1a;
    border-bottom: 1px solid #2a2a2a;
    flex-shrink: 0;
  }

  .console-icon { color: #888; flex-shrink: 0; }
  .console-title { font-size: 0.78rem; font-weight: 600; color: #aaa; flex-shrink: 0; }
  .entry-count { font-size: 0.7rem; color: #555; font-family: ui-monospace, monospace; }

  .console-actions { display: flex; gap: 0.3rem; margin-left: auto; }

  .hdr-btn {
    padding: 0.15rem 0.5rem;
    border-radius: 4px;
    border: 1px solid #333;
    background: transparent;
    color: #666;
    font-size: 0.72rem;
    cursor: pointer;
  }
  .hdr-btn:hover { background: #2a2a2a; color: #aaa; }
  .hdr-btn.close { padding: 0.15rem 0.4rem; }

  .console-body {
    flex: 1;
    overflow-y: auto;
    padding: 0.3rem 0;
  }

  .empty-hint {
    display: block;
    padding: 0.5rem 0.75rem;
    font-size: 0.75rem;
    color: #444;
    font-style: italic;
  }

  /* ── info header (one per RPC call) ── */
  .log-info {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.25rem 0.75rem 0.15rem;
    margin-top: 0.3rem;
    border-top: 1px solid #222;
  }
  .log-info-label {
    font-size: 0.72rem;
    font-weight: 600;
    color: #888;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .log-info-ts {
    font-size: 0.68rem;
    color: #3a3a3a;
    margin-left: auto;
  }

  /* ── command line ── */
  .log-line {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.1rem 0.75rem 0.1rem 1.5rem;
    font-size: 0.76rem;
    line-height: 1.5;
    white-space: nowrap;
    overflow: hidden;
  }
  .log-line:hover { background: rgba(255,255,255,0.03); }

  .log-ts {
    color: #444;
    flex-shrink: 0;
    font-size: 0.7rem;
    user-select: none;
  }

  .log-cmd {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .log-git  { color: #555; }
  .log-sub  { color: #4ec9b0; font-weight: 600; }
  .log-rest { color: #aaa; }

  .log-branch {
    flex-shrink: 0;
    font-size: 0.68rem;
    color: #4a7ef5;
    background: rgba(74,126,245,0.12);
    padding: 0.02rem 0.35rem;
    border-radius: 3px;
    font-family: ui-monospace, monospace;
  }

  .log-ms {
    flex-shrink: 0;
    font-size: 0.68rem;
    color: #3a3a3a;
    font-family: ui-monospace, monospace;
    min-width: 3rem;
    text-align: right;
  }

  /* ── AI CLI invocation line (M16/M16b) ── */
  .log-ai {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.1rem 0.75rem 0.1rem 1.5rem;
    font-size: 0.76rem;
    line-height: 1.5;
    white-space: nowrap;
    overflow: hidden;
  }
  .log-ai:hover { background: rgba(255,255,255,0.03); }
  .log-ai.log-ai-error { background: rgba(239,83,80,0.07); }

  .ai-icon { flex-shrink: 0; font-size: 0.8rem; }

  .ai-cmd {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .ai-name { color: #c792ea; font-weight: 600; }
  .ai-args { color: #aaa; }

  .ai-prompt {
    flex-shrink: 0;
    font-size: 0.68rem;
    color: #555;
    font-family: ui-monospace, monospace;
  }

  .ai-status {
    flex-shrink: 0;
    font-weight: 700;
  }
  .ai-status.ok { color: #6bcf6b; }
  .ai-status.err { color: #ef5350; cursor: help; }

  .ai-cost {
    flex-shrink: 0;
    font-size: 0.68rem;
    color: #d7ba7d;
    font-family: ui-monospace, monospace;
  }

  .log-ai-error-detail {
    padding: 0.05rem 0.75rem 0.3rem 3.2rem;
    font-size: 0.68rem;
    color: #ef5350;
    white-space: pre-wrap;
    word-break: break-word;
  }
</style>
