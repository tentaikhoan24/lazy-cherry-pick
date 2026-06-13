<script lang="ts">
  export interface ToastItem {
    id: number;
    type: "success" | "warning" | "error";
    message: string;
    detail?: string;
  }

  interface Props {
    toasts: ToastItem[];
    ondismiss: (id: number) => void;
    /** Called when the user clicks a URL in the detail — typically opens in browser. */
    onurlclick?: (url: string) => void;
  }

  let { toasts, ondismiss, onurlclick }: Props = $props();

  /** Returns the detail string IF it looks like a single URL (no surrounding text). */
  function detailUrl(detail: string | undefined): string | null {
    if (!detail) return null;
    const trimmed = detail.trim();
    if (/^https?:\/\/\S+$/.test(trimmed)) return trimmed;
    return null;
  }
</script>

{#if toasts.length > 0}
  <div class="toast-container">
    {#each toasts as t (t.id)}
      <div class="toast" class:success={t.type === "success"} class:warning={t.type === "warning"} class:error={t.type === "error"}>
        <span class="icon">
          {t.type === "success" ? "✅" : t.type === "warning" ? "⚠️" : "🔴"}
        </span>
        <div class="body">
          <span class="message">{t.message}</span>
          {#if t.detail}
            {@const url = detailUrl(t.detail)}
            {#if url && onurlclick}
              <button type="button" class="detail detail-link" onclick={() => onurlclick(url)} title="Open in browser">
                {url} ↗
              </button>
            {:else}
              <span class="detail">{t.detail}</span>
            {/if}
          {/if}
        </div>
        <button class="dismiss" onclick={() => ondismiss(t.id)} aria-label="Dismiss">✕</button>
      </div>
    {/each}
  </div>
{/if}

<style>
  .toast-container {
    position: fixed;
    bottom: 1.25rem;
    right: 1.25rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    z-index: 1000;
    max-width: 420px;
    pointer-events: none;
  }
  .toast {
    display: flex;
    align-items: flex-start;
    gap: 0.5rem;
    padding: 0.7rem 1rem;
    border-radius: 8px;
    font-size: 0.875rem;
    box-shadow: 0 4px 20px rgba(0, 0, 0, 0.45);
    animation: slide-in 0.18s ease;
    pointer-events: all;
  }
  @keyframes slide-in {
    from { transform: translateX(110%); opacity: 0; }
    to   { transform: translateX(0);    opacity: 1; }
  }
  .toast.success { background: #1b3a1f; color: #d4f5d8; border: 1px solid #2d6e34; }
  .toast.warning { background: #3a2e1a; color: #f5e4c0; border: 1px solid #7a5c2a; }
  .toast.error   { background: #3a1b1b; color: #f5d4d4; border: 1px solid #7a2e2e; }
  .icon { flex-shrink: 0; font-size: 0.9rem; }
  .body {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    min-width: 0;
  }
  .message { line-height: 1.4; }
  .detail {
    font-size: 0.78rem;
    opacity: 0.75;
    font-family: ui-monospace, monospace;
    white-space: pre-wrap;
    word-break: break-all;
  }
  button.detail.detail-link {
    background: none;
    border: none;
    padding: 0;
    text-align: left;
    color: inherit;
    cursor: pointer;
    opacity: 0.9;
    text-decoration: underline;
    text-underline-offset: 2px;
  }
  button.detail.detail-link:hover { opacity: 1; }
  .dismiss {
    flex-shrink: 0;
    background: none;
    border: none;
    cursor: pointer;
    opacity: 0.5;
    font-size: 0.8rem;
    padding: 0.1rem 0.25rem;
    color: inherit;
    border-radius: 3px;
    line-height: 1;
  }
  .dismiss:hover { opacity: 1; }
</style>
