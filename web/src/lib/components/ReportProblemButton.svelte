<script lang="ts">
  // "Report a problem" — a button plus a modal dialog that files a report
  // (POST /items/{id}/issues) and shows the caller's earlier reports for the
  // item (GET /items/{id}/issues), so a second visit says "You reported this
  // 2 days ago" instead of inviting a duplicate. Admins see the reports on
  // Settings ▸ Library health.
  //
  // The dialog is a native <dialog> opened with showModal(): it renders in
  // the top layer, so it stays visible over a fullscreen player, and traps
  // focus / closes on Escape natively.
  import { issuesApi, type IssueKind, type MediaIssue } from '$lib/api';
  import {
    ISSUE_KIND_OPTIONS,
    ISSUE_NOTE_MAX,
    describeIssue,
    noteRemaining,
    openKinds,
    reportErrorMessage,
    visibleHistory,
  } from '$lib/reportProblem';

  interface Props {
    itemId: string;
    /** The media file being played, when known (player variant). */
    fileId?: string;
    /** Shown under the dialog title, e.g. "Andor — S02E05". */
    itemLabel?: string;
    /** 'overlay' = icon button for the dark player controls; 'detail' =
     *  labelled pill for an item page's action row. */
    variant?: 'overlay' | 'detail';
  }

  let { itemId, fileId, itemLabel = '', variant = 'detail' }: Props = $props();

  const uid = Math.random().toString(36).slice(2, 8);

  let open = $state(false);
  let dialogEl = $state<HTMLDialogElement>();
  let history = $state<MediaIssue[]>([]);
  let loadingHistory = $state(false);
  let kind = $state<IssueKind | ''>('');
  let note = $state('');
  let submitting = $state(false);
  let error = $state('');
  let sent = $state(false);
  let now = $state(Date.now());
  // Guards against a slow history response landing after the dialog was
  // closed and reopened (or the item changed).
  let loadSeq = 0;

  let taken = $derived(openKinds(history));
  let shown = $derived(visibleHistory(history, now));
  let remaining = $derived(noteRemaining(note));
  let canSubmit = $derived(kind !== '' && !taken.has(kind as IssueKind) && !submitting && remaining >= 0);

  async function loadHistory() {
    const seq = ++loadSeq;
    loadingHistory = true;
    try {
      const list = await issuesApi.listMine(itemId);
      if (seq !== loadSeq) return;
      history = list ?? [];
      now = Date.now();
    } catch {
      // The history is a courtesy; the report can still be sent.
      if (seq === loadSeq) history = [];
    } finally {
      if (seq === loadSeq) loadingHistory = false;
    }
  }

  function openDialog() {
    open = true;
    sent = false;
    error = '';
    kind = '';
    note = '';
    history = [];
    void loadHistory();
  }

  function close() {
    loadSeq++;
    open = false;
    if (dialogEl?.open) dialogEl.close();
  }

  // Show the dialog once it's rendered open, and keep typing inside it from
  // reaching the host page: the player binds Space / F / M / arrows on
  // window. (Only keydown is stopped natively — nothing inside handles it
  // through Svelte's delegation; clicks are stopped by the wrapper below,
  // after delegated handlers have run.)
  $effect(() => {
    const el = dialogEl;
    if (!el || !open) return;
    if (!el.open) {
      if (typeof el.showModal === 'function') el.showModal();
      else el.setAttribute('open', '');
    }
    const stop = (e: Event) => e.stopPropagation();
    el.addEventListener('keydown', stop);
    return () => el.removeEventListener('keydown', stop);
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!canSubmit || kind === '') return;
    submitting = true;
    error = '';
    try {
      const trimmed = note.trim();
      const created = await issuesApi.create(itemId, {
        kind,
        ...(trimmed ? { note: trimmed } : {}),
        ...(fileId ? { file_id: fileId } : {}),
      });
      history = [created, ...history];
      sent = true;
    } catch (err) {
      error = reportErrorMessage(err);
      // Someone (another tab) already filed it: refresh so the kind shows
      // as reported.
      if ((err as { code?: string })?.code === 'ALREADY_REPORTED') void loadHistory();
    } finally {
      submitting = false;
    }
  }

  function onBackdropClick(e: MouseEvent) {
    // A click on the <dialog> element itself (not its content) is the
    // backdrop.
    if (e.target === dialogEl) close();
  }
</script>

<!-- Clicks inside must not reach the host: the player's controls overlay
     toggles play/pause on click. -->
<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="report-problem {variant}" onclick={(e) => e.stopPropagation()}>
  <button
    type="button"
    class="trigger"
    aria-haspopup="dialog"
    title="Report a problem"
    aria-label="Report a problem"
    onclick={openDialog}
  >
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="15" height="15" aria-hidden="true">
      <path d="M4 22V4a1 1 0 0 1 1-1h11l-1.5 4L16 11H5" />
    </svg>
    {#if variant === 'detail'}<span>Report a problem</span>{/if}
  </button>

  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <dialog
    bind:this={dialogEl}
    class="rp-dialog"
    aria-labelledby="rp-title-{uid}"
    onclose={() => { loadSeq++; open = false; }}
    onclick={onBackdropClick}
  >
    {#if open}
      <div class="rp-body">
        <h2 id="rp-title-{uid}">Report a problem</h2>
        {#if itemLabel}<p class="rp-subject">{itemLabel}</p>{/if}

        {#if sent}
          <p class="rp-sent" role="status">Thanks — an admin has been notified and will take a look.</p>
          <div class="rp-actions">
            <button type="button" class="rp-primary" onclick={close}>Close</button>
          </div>
        {:else}
          {#if loadingHistory}
            <p class="rp-muted">Checking your earlier reports…</p>
          {:else if shown.length}
            <ul class="rp-history" aria-label="Your earlier reports">
              {#each shown as issue (issue.id)}
                <li class:open={issue.status === 'open'}>{describeIssue(issue, now)}</li>
              {/each}
            </ul>
          {/if}

          <form onsubmit={submit}>
            <fieldset class="rp-kinds">
              <legend>What's wrong?</legend>
              {#each ISSUE_KIND_OPTIONS as opt (opt.value)}
                <label class="rp-kind" class:taken={taken.has(opt.value)}>
                  <input
                    type="radio"
                    name="rp-kind-{uid}"
                    value={opt.value}
                    bind:group={kind}
                    disabled={taken.has(opt.value)}
                  />
                  <span>{opt.label}</span>
                  {#if taken.has(opt.value)}<span class="rp-taken">already reported</span>{/if}
                </label>
              {/each}
            </fieldset>

            <label class="rp-note-label" for="rp-note-{uid}">Details <span class="rp-muted">(optional)</span></label>
            <textarea
              id="rp-note-{uid}"
              class="rp-note"
              rows="3"
              maxlength={ISSUE_NOTE_MAX}
              placeholder="What happened, and roughly when in the video?"
              bind:value={note}
            ></textarea>
            <div class="rp-counter" class:over={remaining < 0}>{remaining} characters left</div>

            {#if error}<p class="rp-error" role="alert">{error}</p>{/if}

            <div class="rp-actions">
              <button type="button" class="rp-secondary" onclick={close}>Cancel</button>
              <button type="submit" class="rp-primary" disabled={!canSubmit}>
                {submitting ? 'Sending…' : 'Send report'}
              </button>
            </div>
          </form>
        {/if}
      </div>
    {/if}
  </dialog>
</div>

<style>
  .report-problem {
    position: relative;
    display: inline-flex;
    align-items: center;
  }
  .report-problem.detail {
    margin-left: 0.5rem;
    vertical-align: middle;
  }

  .trigger {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  /* Player controls: same look as the page's .icon-btn.small. */
  .overlay .trigger {
    background: none;
    border: none;
    color: rgba(255, 255, 255, 0.9);
    padding: 0.3rem;
    border-radius: 6px;
  }
  .overlay .trigger:hover {
    background: rgba(255, 255, 255, 0.1);
    color: #fff;
  }
  /* Item page: matches the Download / Play on pills next to it. */
  .detail .trigger {
    background: var(--input-bg);
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    color: var(--text-muted);
    font-size: 0.75rem;
    font-weight: 500;
    padding: 0.35rem 0.7rem;
  }
  .detail .trigger:hover {
    color: var(--text-secondary);
    background: var(--bg-hover);
  }

  .rp-dialog {
    width: min(440px, calc(100vw - 2rem));
    max-height: calc(100vh - 2rem);
    padding: 0;
    border: 1px solid var(--border, rgba(255, 255, 255, 0.12));
    border-radius: 10px;
    background: var(--bg-elevated, var(--bg-secondary, #16161f));
    color: var(--text-primary, #eee);
    box-shadow: 0 12px 40px rgba(0, 0, 0, 0.45);
  }
  .rp-dialog::backdrop {
    background: rgba(0, 0, 0, 0.55);
  }
  .rp-body {
    padding: 1.1rem 1.2rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    text-align: left;
  }
  h2 {
    margin: 0;
    font-size: 1.05rem;
    font-weight: 600;
  }
  .rp-subject {
    margin: -0.35rem 0 0;
    font-size: 0.8rem;
    color: var(--text-muted, #999);
  }
  .rp-muted {
    color: var(--text-muted, #999);
    font-size: 0.78rem;
    margin: 0;
  }
  .rp-history {
    list-style: none;
    margin: 0;
    padding: 0.5rem 0.7rem;
    border-radius: 6px;
    background: var(--bg-hover, rgba(255, 255, 255, 0.05));
    font-size: 0.78rem;
    color: var(--text-secondary, #ccc);
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .rp-history li.open {
    color: var(--text-primary, #eee);
  }
  form {
    display: flex;
    flex-direction: column;
    gap: 0.55rem;
  }
  .rp-kinds {
    border: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
  }
  legend {
    font-size: 0.8rem;
    font-weight: 600;
    margin-bottom: 0.3rem;
    padding: 0;
  }
  .rp-kind {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
    padding: 0.3rem 0.4rem;
    border-radius: 5px;
    cursor: pointer;
  }
  .rp-kind:hover:not(.taken) {
    background: var(--bg-hover, rgba(255, 255, 255, 0.06));
  }
  .rp-kind.taken {
    cursor: default;
    opacity: 0.6;
  }
  .rp-taken {
    margin-left: auto;
    font-size: 0.7rem;
    color: var(--text-muted, #999);
  }
  .rp-note-label {
    font-size: 0.8rem;
    font-weight: 600;
  }
  .rp-note {
    resize: vertical;
    min-height: 4rem;
    font: inherit;
    font-size: 0.85rem;
    padding: 0.45rem 0.55rem;
    border-radius: 6px;
    border: 1px solid var(--border-strong, rgba(255, 255, 255, 0.2));
    background: var(--input-bg, rgba(0, 0, 0, 0.2));
    color: inherit;
  }
  .rp-counter {
    align-self: flex-end;
    font-size: 0.7rem;
    color: var(--text-muted, #999);
    margin-top: -0.35rem;
  }
  .rp-counter.over,
  .rp-error {
    color: #f87171;
  }
  .rp-error {
    margin: 0;
    font-size: 0.8rem;
  }
  .rp-sent {
    margin: 0.2rem 0;
    font-size: 0.9rem;
  }
  .rp-actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
    margin-top: 0.2rem;
  }
  .rp-primary,
  .rp-secondary {
    font: inherit;
    font-size: 0.82rem;
    padding: 0.4rem 0.9rem;
    border-radius: 6px;
    cursor: pointer;
  }
  .rp-primary {
    background: var(--accent, #7c6af7);
    color: #fff;
    border: none;
  }
  .rp-primary:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .rp-secondary {
    background: none;
    border: 1px solid var(--border-strong, rgba(255, 255, 255, 0.2));
    color: inherit;
  }
</style>
