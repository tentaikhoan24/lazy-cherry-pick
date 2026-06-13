<script lang="ts">
  // M14 — Connect a repo to a forge (GitHub/GitLab). Modal opened from Settings
  // (or from a "Connect" suggestion banner when openRepo auto-detects a
  // recognized remote URL but no connection is configured).
  //
  // Flow: user picks kind → enters token → clicks Test → on success, Save
  // commits the connection and stores the token in the OS keychain via Rust.

  import { rpc } from "./rpc";
  import { parseForge } from "./forge";
  import type { Remote, ForgeKind } from "./rpc-types";
  import { openUrl } from "@tauri-apps/plugin-opener";

  interface Props {
    repoPath: string;
    /** Used to auto-detect kind + baseURL + host on open. */
    remotes: Remote[];
    /** "origin" by default — pick the remote URL to derive defaults from. */
    sourceRemote?: string;
    onclose: () => void;
    onsaved: () => void;
  }

  let { repoPath, remotes, sourceRemote = "origin", onclose, onsaved }: Props = $props();

  // Pre-populate from the detected forge if possible.
  function defaultsFromRemotes() {
    const r = remotes.find((x) => x.name === sourceRemote) ?? remotes[0];
    if (!r) return { kind: "github" as ForgeKind, baseUrl: "https://api.github.com", host: "github.com" };
    const info = parseForge(r.pushUrl || r.fetchUrl);
    if (!info || info.kind === "unknown") {
      return { kind: "github" as ForgeKind, baseUrl: "https://api.github.com", host: "github.com" };
    }
    if (info.kind === "github") {
      // For GitHub Enterprise, the API base differs ("/api/v3"); leave it as
      // .com unless the user changes it. Cloud GitHub uses api.github.com.
      const isCloud = info.host === "github.com";
      return {
        kind: "github" as ForgeKind,
        baseUrl: isCloud ? "https://api.github.com" : `https://${info.host}/api/v3`,
        host: info.host,
      };
    }
    if (info.kind === "gitlab") {
      return {
        kind: "gitlab" as ForgeKind,
        baseUrl: `https://${info.host}`,
        host: info.host,
      };
    }
    if (info.kind === "bitbucket") {
      return {
        kind: "bitbucket" as ForgeKind,
        baseUrl: "https://api.bitbucket.org",
        host: info.host,
      };
    }
    return { kind: "github" as ForgeKind, baseUrl: "https://api.github.com", host: "github.com" };
  }

  function defaultBaseUrl(k: ForgeKind): string {
    if (k === "github") return "https://api.github.com";
    if (k === "gitlab") return "https://gitlab.com";
    return "https://api.bitbucket.org";
  }
  function defaultHost(k: ForgeKind): string {
    if (k === "github") return "github.com";
    if (k === "gitlab") return "gitlab.com";
    return "bitbucket.org";
  }

  const initialDefaults = defaultsFromRemotes();
  let kind = $state<ForgeKind>(initialDefaults.kind);
  let baseUrl = $state(initialDefaults.baseUrl);
  let host = $state(initialDefaults.host);
  /** Bitbucket Basic Auth needs the username up-front (no /user-only lookup
   * without it). For GitHub/GitLab this is auto-populated from the test result
   * and the field is hidden in the form. */
  let username = $state("");
  let token = $state("");
  let testing = $state(false);
  let saving = $state(false);
  let testResult = $state<{ username: string; scopes: string[] } | null>(null);
  let error = $state("");

  const needsUsernameUpfront = $derived(kind === "bitbucket");
  const tokenLabel = $derived(kind === "bitbucket" ? "API Token" : "Personal Access Token");
  const tokenPlaceholder = $derived(
    kind === "github" ? "ghp_xxx…" :
    kind === "gitlab" ? "glpat-xxx…" :
    "ATATT3xFfGF0…"
  );
  const usernameLabel = $derived(kind === "bitbucket" ? "Atlassian account email" : "Username");
  const usernamePlaceholder = $derived(kind === "bitbucket" ? "you@example.com" : "your-handle");

  // When kind changes, reset baseURL/host to that provider's defaults — but
  // only if the user hasn't touched them (heuristic: matches the previous default).
  let previousDefault = $state(initialDefaults.baseUrl);
  $effect(() => {
    if (baseUrl === previousDefault) {
      const fresh = defaultBaseUrl(kind);
      baseUrl = fresh;
      host = defaultHost(kind);
      previousDefault = fresh;
    }
  });

  async function testConnection() {
    if (!token.trim()) {
      error = "Token is required";
      return;
    }
    if (needsUsernameUpfront && !username.trim()) {
      error = "Username is required for Bitbucket";
      return;
    }
    testing = true;
    error = "";
    testResult = null;
    try {
      testResult = await rpc.forge.testConnection(
        kind,
        baseUrl,
        token,
        needsUsernameUpfront ? username.trim() : undefined,
      );
    } catch (e) {
      error = String(e);
    } finally {
      testing = false;
    }
  }

  async function save() {
    if (!testResult) {
      error = "Test the connection first";
      return;
    }
    saving = true;
    error = "";
    try {
      // For Bitbucket, the user-entered username is what we used for Basic Auth.
      // For GitHub/GitLab, take it from testResult (returned by /user API).
      const finalUsername = needsUsernameUpfront ? username.trim() : testResult.username;
      await rpc.forge.saveConnection({
        repoPath,
        kind,
        baseUrl,
        host,
        username: finalUsername,
        token,
      });
      onsaved();
      onclose();
    } catch (e) {
      error = String(e);
    } finally {
      saving = false;
    }
  }

  function tokenHelpUrl(): string {
    if (kind === "github") return "https://github.com/settings/tokens";
    if (kind === "gitlab") return "https://gitlab.com/-/user_settings/personal_access_tokens";
    // Atlassian API tokens with scopes — replaces App Passwords (removed 2026-07-28).
    return "https://id.atlassian.com/manage-profile/security/api-tokens";
  }

  function requiredScopes(): string {
    if (kind === "github") return "repo (full)";
    if (kind === "gitlab") return "api";
    return "read:user:bitbucket, read:repository:bitbucket, read:pullrequest:bitbucket, write:pullrequest:bitbucket";
  }

  async function openTokenPage(e: MouseEvent) {
    e.preventDefault();
    try {
      await openUrl(tokenHelpUrl());
    } catch {
      // ignore — opener might be blocked in some envs
    }
  }
</script>

<div class="overlay" onclick={onclose} role="presentation">
  <div class="modal" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true">
    <div class="modal-header">
      <h2>Connect this repo to a forge</h2>
      <button class="close-btn" onclick={onclose} aria-label="Close">×</button>
    </div>

    <div class="modal-body">
      <div class="row">
        <label>Provider</label>
        <div class="segmented">
          <button class:active={kind === "github"} onclick={() => (kind = "github")}>GitHub</button>
          <button class:active={kind === "gitlab"} onclick={() => (kind = "gitlab")}>GitLab</button>
          <button class:active={kind === "bitbucket"} onclick={() => (kind = "bitbucket")}>Bitbucket</button>
        </div>
      </div>

      <div class="row">
        <label for="base-url">API base URL</label>
        <input
          id="base-url"
          type="text"
          bind:value={baseUrl}
          placeholder={defaultBaseUrl(kind)}
          spellcheck="false"
        />
        <p class="hint">
          {#if kind === "github"}For GitHub Enterprise, use your instance URL.{/if}
          {#if kind === "gitlab"}For self-managed GitLab, use your instance URL.{/if}
          {#if kind === "bitbucket"}Bitbucket Cloud only — self-hosted Bitbucket Server has a different URL pattern.{/if}
        </p>
      </div>

      <div class="row">
        <label for="host">Host (for display)</label>
        <input id="host" type="text" bind:value={host} spellcheck="false" />
      </div>

      {#if needsUsernameUpfront}
        <div class="row">
          <label for="username">{usernameLabel}</label>
          <input
            id="username"
            type="email"
            bind:value={username}
            placeholder={usernamePlaceholder}
            spellcheck="false"
            autocomplete="off"
          />
          <p class="hint">
            Used as the username for Basic Auth with the API token.
          </p>
        </div>
      {/if}

      <div class="row">
        <label for="token">{tokenLabel}</label>
        <input
          id="token"
          type="password"
          bind:value={token}
          placeholder={tokenPlaceholder}
          spellcheck="false"
          autocomplete="off"
        />
        <p class="hint">
          Required scopes: <code>{requiredScopes()}</code> ·
          <a href={tokenHelpUrl()} onclick={openTokenPage}>Create {kind === "bitbucket" ? "API token" : "token"} →</a>
        </p>
        {#if kind === "bitbucket"}
          <p class="hint warn">
            ⚠ Atlassian is removing App Passwords on 2026-07-28. Use an API Token with scopes (link above).
          </p>
        {/if}
      </div>

      {#if error}
        <div class="error">{error}</div>
      {/if}

      {#if testResult}
        <div class="success">
          ✓ Connected as <strong>{testResult.username}</strong>
          {#if testResult.scopes.length > 0}
            <span class="scopes">
              · scopes: {testResult.scopes.join(", ")}
            </span>
          {/if}
        </div>
      {/if}
    </div>

    <div class="modal-footer">
      <button class="cancel-btn" onclick={onclose} disabled={saving}>Cancel</button>
      <button class="test-btn" onclick={testConnection} disabled={testing || saving || !token.trim()}>
        {testing ? "Testing…" : "Test connection"}
      </button>
      <button class="save-btn" onclick={save} disabled={!testResult || saving}>
        {saving ? "Saving…" : "Save & connect"}
      </button>
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
    z-index: 500;
  }
  .modal {
    width: min(520px, 92vw);
    background: var(--surface-elevated, #2c2c2c);
    border: 1px solid var(--border, #3a3a3a);
    border-radius: 8px;
    box-shadow: 0 14px 40px rgba(0,0,0,0.5);
    display: flex;
    flex-direction: column;
    max-height: 90vh;
  }
  .modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.85rem 1.1rem;
    border-bottom: 1px solid var(--border, #3a3a3a);
  }
  .modal-header h2 {
    margin: 0;
    font-size: 0.95rem;
    font-weight: 600;
    color: var(--text, #f0f0f0);
  }
  .close-btn {
    background: none;
    border: none;
    font-size: 1.4rem;
    line-height: 1;
    color: var(--text-muted, #888);
    cursor: pointer;
    padding: 0 0.3rem;
  }
  .close-btn:hover { color: var(--text, #f0f0f0); }

  .modal-body {
    padding: 1rem 1.1rem;
    display: flex;
    flex-direction: column;
    gap: 0.85rem;
    overflow-y: auto;
  }
  .row {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .row label {
    font-size: 0.78rem;
    font-weight: 600;
    color: var(--text-secondary, #ccc);
  }
  .row input {
    padding: 0.35rem 0.55rem;
    border: 1px solid var(--border, #555);
    border-radius: 5px;
    background: var(--input-bg, #1e1e1e);
    color: var(--text, #f0f0f0);
    font-size: 0.85rem;
    font-family: ui-monospace, monospace;
  }
  .row input:focus { outline: none; border-color: var(--accent, #4a7ef5); }
  .hint {
    margin: 0;
    font-size: 0.72rem;
    color: var(--text-muted, #888);
  }
  .hint.warn {
    color: #ffc850;
    font-weight: 500;
  }
  .hint code {
    background: var(--hover, #2e2e2e);
    padding: 0.05rem 0.3rem;
    border-radius: 3px;
    color: var(--text-secondary, #ccc);
  }
  .hint a { color: var(--accent, #4a7ef5); text-decoration: none; }
  .hint a:hover { text-decoration: underline; }

  .segmented {
    display: inline-flex;
    border: 1px solid var(--border, #555);
    border-radius: 5px;
    overflow: hidden;
    align-self: flex-start;
  }
  .segmented button {
    background: var(--input-bg, #1e1e1e);
    color: var(--text-secondary, #ccc);
    border: none;
    padding: 0.35rem 0.85rem;
    font-size: 0.82rem;
    cursor: pointer;
  }
  .segmented button.active {
    background: var(--accent, #4a7ef5);
    color: white;
    font-weight: 600;
  }

  .error {
    background: rgba(224, 85, 85, 0.12);
    border: 1px solid #e05555;
    border-radius: 5px;
    color: #ff8888;
    padding: 0.45rem 0.7rem;
    font-size: 0.8rem;
    word-break: break-word;
  }
  .success {
    background: rgba(80, 200, 120, 0.1);
    border: 1px solid #50c878;
    border-radius: 5px;
    color: #8fe1a3;
    padding: 0.45rem 0.7rem;
    font-size: 0.82rem;
  }
  .scopes {
    color: var(--text-muted, #888);
    font-size: 0.75rem;
    margin-left: 0.3rem;
  }

  .modal-footer {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
    padding: 0.7rem 1.1rem;
    border-top: 1px solid var(--border, #3a3a3a);
  }
  .modal-footer button {
    padding: 0.4rem 0.95rem;
    border-radius: 5px;
    border: 1px solid var(--border, #555);
    background: var(--input-bg, #2a2a2a);
    color: var(--text, #f0f0f0);
    font-size: 0.85rem;
    cursor: pointer;
  }
  .modal-footer button:disabled { opacity: 0.45; cursor: not-allowed; }
  .modal-footer .save-btn {
    background: var(--accent, #4a7ef5);
    border-color: var(--accent, #4a7ef5);
    color: white;
    font-weight: 600;
  }
  .modal-footer .save-btn:not(:disabled):hover { filter: brightness(1.1); }
</style>
