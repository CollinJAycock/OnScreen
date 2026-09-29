<script lang="ts">
  // "Report a problem" from an item page (POST /api/v1/items/{id}/issues).
  // Two steps in one D-pad dialog: pick what's wrong, then send — with an
  // optional note typed on the on-screen keyboard. Kinds the user already
  // has an open report for are marked (the server refuses a second one).
  // Back steps out of the keyboard, then back to the kinds, then closes.
  import { onMount, tick } from 'svelte';
  import { endpoints, type IssueKind } from '$lib/api';
  import { focusable, focusScope } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import OnScreenKeyboard from '$lib/components/OnScreenKeyboard.svelte';
  import {
    ALREADY_REPORTED_TEXT,
    ISSUE_KIND_OPTIONS,
    REPORT_SENT_TEXT,
    clampNote,
    issueKindLabel,
    noteRemaining,
    reportErrorMessage,
  } from '$lib/reportProblem';

  interface Props {
    itemId: string;
    itemTitle: string;
    fileId?: string;
    /** Kinds with an open report already (from GET …/issues). */
    openKinds: Set<IssueKind>;
    /** Closed; `reported` is the kind just sent, if any. */
    onclose: (reported?: IssueKind) => void;
  }
  let { itemId, itemTitle, fileId, openKinds, onclose }: Props = $props();

  let step = $state<'kind' | 'note' | 'done'>('kind');
  let kind = $state<IssueKind | null>(null);
  let note = $state('');
  let editingNote = $state(false);
  let sending = $state(false);
  let message = $state('');
  let sent = $state<IssueKind | undefined>(undefined);
  // Kinds refused as already reported during this dialog, on top of openKinds.
  let refused = $state<IssueKind[]>([]);

  let root: HTMLElement | undefined = $state();
  let sendBtn: HTMLElement | undefined = $state();

  const firstOpenKind = $derived(
    Math.max(0, ISSUE_KIND_OPTIONS.findIndex((o) => !isReported(o.value))),
  );
  const remaining = $derived(noteRemaining(note));

  function isReported(k: IssueKind): boolean {
    return openKinds.has(k) || refused.includes(k);
  }

  onMount(() =>
    focusManager.pushBack(() => {
      if (sending) return true;
      if (step === 'note') {
        if (editingNote) void stopEditing();
        else void backToKinds();
        return true;
      }
      onclose(sent);
      return true;
    }),
  );

  function focusFirstIn(sel: string) {
    const el = root?.querySelector<HTMLElement>(`${sel} [data-focusable]`);
    if (el) focusManager.focus(el);
  }

  function pickKind(k: IssueKind) {
    if (isReported(k)) {
      message = ALREADY_REPORTED_TEXT;
      return;
    }
    kind = k;
    message = '';
    step = 'note';
  }

  async function backToKinds() {
    step = 'kind';
    message = '';
    await tick();
    focusFirstIn('.kinds');
  }

  async function startEditing() {
    editingNote = true;
    await tick();
    focusFirstIn('.note-keyboard');
  }

  async function stopEditing() {
    editingNote = false;
    await tick();
    if (sendBtn) focusManager.focus(sendBtn);
  }

  async function send() {
    if (!kind || sending) return;
    sending = true;
    message = '';
    try {
      const trimmed = note.trim();
      await endpoints.issues.create(itemId, {
        kind,
        ...(trimmed ? { note: trimmed } : {}),
        ...(fileId ? { file_id: fileId } : {}),
      });
      sent = kind;
      step = 'done';
      message = REPORT_SENT_TEXT;
    } catch (e) {
      message = reportErrorMessage(e);
      if ((e as { code?: string })?.code === 'ALREADY_REPORTED') refused = [...refused, kind];
    } finally {
      sending = false;
    }
  }
</script>

<div
  class="dialog-backdrop"
  role="dialog"
  aria-modal="true"
  aria-labelledby="report-title"
  use:focusScope
>
  <div class="dialog" bind:this={root}>
    <div class="dialog-title" id="report-title">Report a problem</div>
    <div class="dialog-sub">{itemTitle}</div>

    {#if step === 'kind'}
      <div class="dialog-body">What's wrong?</div>
      <div class="kinds">
        {#each ISSUE_KIND_OPTIONS as opt, i (opt.value)}
          <button
            use:focusable={{ autofocus: i === firstOpenKind }}
            class="dialog-btn"
            class:muted={isReported(opt.value)}
            onclick={() => pickKind(opt.value)}
          >
            {opt.label}{#if isReported(opt.value)}<span class="tag"> — already reported</span>{/if}
          </button>
        {/each}
        <button use:focusable class="dialog-btn" onclick={() => onclose(sent)}>Cancel</button>
      </div>
    {:else if step === 'note' && kind}
      <div class="dialog-body">What's wrong: <strong>{issueKindLabel(kind)}</strong></div>
      {#if editingNote}
        <div class="note-keyboard">
          <OnScreenKeyboard
            bind:value={note}
            onchange={(v) => (note = clampNote(v))}
            onsubmit={() => void stopEditing()}
          />
          <div class="hint">
            Optional note for the admin · "done" or Back when finished
            {#if remaining < 100}· {remaining} characters left{/if}
          </div>
        </div>
      {:else}
        <div class="note-preview" class:empty={!note.trim()}>
          {note.trim() || 'No note — add one if it helps the admin find the problem.'}
        </div>
        <div class="actions">
          <button
            use:focusable={{ autofocus: true }}
            bind:this={sendBtn}
            class="dialog-btn primary"
            onclick={send}
            disabled={sending}
          >
            {sending ? 'Sending…' : 'Send report'}
          </button>
          <button use:focusable class="dialog-btn" onclick={() => void startEditing()} disabled={sending}>
            {note.trim() ? 'Edit note' : 'Add a note (optional)'}
          </button>
          <button use:focusable class="dialog-btn" onclick={() => void backToKinds()} disabled={sending}>
            Back
          </button>
        </div>
      {/if}
    {:else}
      <div class="actions">
        <button use:focusable={{ autofocus: true }} class="dialog-btn primary" onclick={() => onclose(sent)}>
          Close
        </button>
      </div>
    {/if}

    {#if message}
      <div class="message" class:ok={step === 'done'} role="status">{message}</div>
    {/if}
  </div>
</div>

<style>
  .dialog-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.72);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
  }
  .dialog {
    background: var(--bg-secondary, #1f1f24);
    border: 2px solid var(--border-strong);
    border-radius: 16px;
    padding: 36px 40px;
    width: 960px;
    max-width: 92vw;
    max-height: 1000px;
    box-shadow: 0 12px 48px rgba(0, 0, 0, 0.6);
  }
  .dialog-title {
    font-size: var(--font-lg);
  }
  .dialog-sub {
    font-size: var(--font-sm);
    color: var(--text-secondary);
    margin-top: 4px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .dialog-body {
    font-size: var(--font-md);
    margin: 24px 0 16px;
  }
  .kinds,
  .actions {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .actions {
    margin-top: 20px;
  }
  .dialog-btn {
    font-family: inherit;
    font-size: var(--font-md);
    text-align: left;
    padding: 16px 28px;
    border-radius: 12px;
    border: 2px solid var(--border);
    background: var(--bg-elevated);
    color: var(--text-primary);
    cursor: pointer;
  }
  .dialog-btn.primary {
    background: var(--accent);
    border-color: var(--accent);
    color: #fff;
  }
  .dialog-btn.muted {
    color: var(--text-secondary);
  }
  .dialog-btn:disabled {
    opacity: 0.6;
  }
  .tag {
    font-size: var(--font-sm);
  }
  .note-preview {
    font-size: var(--font-md);
    padding: 16px 20px;
    border-radius: 10px;
    background: var(--bg-primary);
    border: 2px solid var(--border);
    max-height: 200px;
    overflow: hidden;
    word-break: break-word;
  }
  .note-preview.empty {
    color: var(--text-muted);
    font-size: var(--font-sm);
  }
  .hint {
    margin-top: 12px;
    font-size: var(--font-xs);
    color: var(--text-muted);
  }
  .message {
    margin-top: 20px;
    padding: 12px 16px;
    border-radius: 8px;
    font-size: var(--font-sm);
    line-height: 1.45;
    background: rgba(248, 113, 113, 0.12);
    color: #fca5a5;
  }
  .message.ok {
    background: rgba(52, 211, 153, 0.12);
    color: #34d399;
  }
</style>
