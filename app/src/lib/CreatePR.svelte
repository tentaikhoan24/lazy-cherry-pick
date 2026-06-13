<script lang="ts">
  // M14 — Create PR/MR dialog. Opened after a successful "Apply & Push & Create
  // PR" run. Pre-fills title from the last commit subject and body from the
  // bullet list of commit subjects, then lets the user edit before submitting.

  import type { Branch, Commit, ForgeKind } from "./rpc-types";

  interface Props {
    forgeKind: ForgeKind;
    /** owner/repo or namespace/path — derived from the remote URL. */
    projectPath: string;
    /** The branch we just pushed (head of the PR). */
    headBranch: string;
    /** The local target branch — usually the PR base. */
    defaultBase: string;
    /** All branches (local + remote) — for the base dropdown filter. */
    branches: Branch[];
    /** Commits that were just applied (used to pre-fill title + body). */
    appliedCommits: Commit[];
    onclose: () => void;
    /** Resolves with the created PR URL or rejects with an error message. */
    onsubmit: (args: { base: string; head: string; title: string; body: string; draft: boolean }) => Promise<void>;
  }

  let { forgeKind, projectPath, headBranch, defaultBase, branches, appliedCommits, onclose, onsubmit }: Props = $props();

  function defaultTitle(): string {
    if (appliedCommits.length === 0) return "";
    if (appliedCommits.length === 1) return appliedCommits[0].subject;
    return appliedCommits[appliedCommits.length - 1].subject;
  }

  function defaultBody(): string {
    if (appliedCommits.length === 0) return "";
    if (appliedCommits.length === 1) return appliedCommits[0].subject;
    const lines = appliedCommits.map((c) => `- ${c.subject}`);
    return lines.join("\n");
  }

  let base = $state(defaultBase);
  let title = $state(defaultTitle());
  let body = $state(defaultBody());
  let draft = $state(false);
  let submitting = $state(false);
  let error = $state("");

  // Filter to local branches only — you don't PR against a remote-tracking ref.
  const baseOptions = $derived(branches.filter((b) => !b.remote));

  const providerLabel = $derived(
    forgeKind === "github" ? "GitHub PR" :
    forgeKind === "gitlab" ? "GitLab MR" :
    "Bitbucket PR"
  );

  async function submit() {
    if (!title.trim()) {
      error = "Title is required";
      return;
    }
    if (!base) {
      error = "Base branch is required";
      return;
    }
    submitting = true;
    error = "";
    try {
      await onsubmit({ base, head: headBranch, title, body, draft });
      // Parent closes the dialog on success
    } catch (e) {
      error = String(e);
      submitting = false;
    }
  }
</script>

<div class="overlay" onclick={onclose} role="presentation">
  <div class="modal" onclick={(e) => e.stopPropagation()} role="dialog" aria-modal="true">
    <div class="modal-header">
      <h2>Create {providerLabel}</h2>
      <span class="project-path">{projectPath}</span>
      <button class="close-btn" onclick={onclose} aria-label="Close">×</button>
    </div>

    <div class="modal-body">
      <div class="branches-row">
        <div class="branch-block">
          <label for="base-branch">Base</label>
          <select id="base-branch" bind:value={base}>
            {#each baseOptions as b}
              <option value={b.name}>{b.name}</option>
            {/each}
          </select>
        </div>
        <div class="arrow">←</div>
        <div class="branch-block">
          <label>Head</label>
          <div class="head-name">{headBranch}</div>
        </div>
      </div>

      <div class="row">
        <label for="title">Title <span class="required" aria-label="required">*</span></label>
        <input
          id="title"
          type="text"
          bind:value={title}
          placeholder="Short summary"
          spellcheck="false"
          required
        />
      </div>

      <div class="row">
        <label for="body">Description</label>
        <textarea
          id="body"
          bind:value={body}
          rows="8"
          placeholder="What does this change? Why? Link to issues."
          spellcheck="false"
        ></textarea>
      </div>

      <div class="row inline">
        <label>
          <input type="checkbox" bind:checked={draft} />
          Open as draft
        </label>
      </div>

      {#if error}
        <div class="error">{error}</div>
      {/if}
    </div>

    <div class="modal-footer">
      <button class="cancel-btn" onclick={onclose} disabled={submitting}>Cancel</button>
      <button class="submit-btn" onclick={submit} disabled={submitting || !title.trim()}>
        {submitting ? "Creating…" : `Create ${providerLabel}`}
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
    width: min(640px, 94vw);
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
    gap: 0.6rem;
    padding: 0.85rem 1.1rem;
    border-bottom: 1px solid var(--border, #3a3a3a);
  }
  .modal-header h2 {
    margin: 0;
    font-size: 0.95rem;
    font-weight: 600;
    color: var(--text, #f0f0f0);
  }
  .project-path {
    flex: 1;
    font-family: ui-monospace, monospace;
    font-size: 0.78rem;
    color: var(--text-muted, #888);
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
  .branches-row {
    display: flex;
    align-items: center;
    gap: 0.85rem;
    padding: 0.6rem 0.7rem;
    background: var(--hover, #272727);
    border-radius: 5px;
  }
  .branch-block {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }
  .branch-block label {
    font-size: 0.7rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted, #888);
  }
  .branch-block select {
    padding: 0.35rem 0.55rem;
    border: 1px solid var(--border, #555);
    border-radius: 5px;
    background: var(--input-bg, #1e1e1e);
    color: var(--text, #f0f0f0);
    font-size: 0.85rem;
    font-family: ui-monospace, monospace;
  }
  .head-name {
    padding: 0.35rem 0.55rem;
    border: 1px solid var(--border, #555);
    border-radius: 5px;
    background: var(--input-bg, #1e1e1e);
    color: var(--text-secondary, #ccc);
    font-size: 0.85rem;
    font-family: ui-monospace, monospace;
  }
  .arrow {
    color: var(--text-muted, #888);
    font-size: 1.1rem;
    align-self: flex-end;
    padding-bottom: 0.35rem;
  }

  .row {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .row.inline {
    flex-direction: row;
    align-items: center;
  }
  .row.inline label {
    display: flex;
    gap: 0.4rem;
    align-items: center;
    cursor: pointer;
  }
  .row label {
    font-size: 0.78rem;
    font-weight: 600;
    color: var(--text-secondary, #ccc);
  }
  .required {
    color: #e05555;
    font-weight: 700;
    margin-left: 2px;
  }
  .row input[type="text"], .row textarea {
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--border, #555);
    border-radius: 5px;
    background: var(--input-bg, #1e1e1e);
    color: var(--text, #f0f0f0);
    font-size: 0.85rem;
    font-family: inherit;
    resize: vertical;
  }
  .row input[type="text"]:focus, .row textarea:focus {
    outline: none;
    border-color: var(--accent, #4a7ef5);
  }
  .row textarea { min-height: 100px; font-family: ui-monospace, monospace; }

  .error {
    background: rgba(224, 85, 85, 0.12);
    border: 1px solid #e05555;
    border-radius: 5px;
    color: #ff8888;
    padding: 0.45rem 0.7rem;
    font-size: 0.8rem;
    word-break: break-word;
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
  .modal-footer .submit-btn {
    background: var(--accent, #4a7ef5);
    border-color: var(--accent, #4a7ef5);
    color: white;
    font-weight: 600;
  }
  .modal-footer .submit-btn:not(:disabled):hover { filter: brightness(1.1); }
</style>
