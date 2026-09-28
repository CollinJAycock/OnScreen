<script lang="ts">
  // Confirm an admin stop, with an optional note the viewer sees:
  // "Playback was stopped by the server admin: <message>".
  import type { ActiveSession } from '$lib/api';
  import { STOP_MESSAGE_MAX, decisionLabel, stopMessageError, viewerLabel } from './now-playing';

  interface Props {
    session: ActiveSession;
    busy?: boolean;
    /** Server-side failure to show (the dialog stays open). */
    error?: string;
    onconfirm: (message: string) => void;
    oncancel: () => void;
  }

  let { session, busy = false, error = '', onconfirm, oncancel }: Props = $props();

  let message = $state('');
  const invalid = $derived(stopMessageError(message));
  const count = $derived([...message.trim()].length);
  const who = $derived(viewerLabel(session) || session.client_name || 'this viewer');
  // Direct play / remux can simply be restarted by the client, so the server
  // also refuses that device for a couple of minutes; a transcode can't.
  const refuses = $derived(session.decision !== 'transcode');

  function submit(e: SubmitEvent) {
    e.preventDefault();
    if (invalid || busy) return;
    onconfirm(message.trim());
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape' && !busy) oncancel();
  }
</script>

<svelte:window onkeydown={onKey} />

<div class="backdrop" role="presentation" onclick={(e) => { if (e.target === e.currentTarget && !busy) oncancel(); }}>
  <div class="modal" role="dialog" aria-modal="true" aria-labelledby="stop-title">
    <form onsubmit={submit}>
      <h2 id="stop-title">Stop this stream?</h2>
      <p class="summary">
        <strong>{session.title}</strong> for <strong>{who}</strong>{#if session.client_name && viewerLabel(session)} on {session.client_name}{/if}
        <span class="muted">({decisionLabel(session.decision)})</span>
      </p>
      <p class="hint">
        The viewer's player stops and shows your message.
        {#if refuses}That device can't restart this title for about two minutes; their other devices keep working.{/if}
      </p>
      <label for="stop-message">Message to the viewer <span class="muted">(optional)</span></label>
      <textarea id="stop-message" rows="3" bind:value={message} disabled={busy}
                placeholder="e.g. Server maintenance — back in 10 minutes"
                aria-invalid={invalid ? 'true' : undefined}></textarea>
      <div class="counter" class:over={!!invalid}>{count} / {STOP_MESSAGE_MAX}</div>
      {#if invalid}<p class="err" role="alert">{invalid}</p>{/if}
      {#if error}<p class="err" role="alert">{error}</p>{/if}
      <div class="actions">
        <button type="button" class="btn" onclick={oncancel} disabled={busy}>Cancel</button>
        <button type="submit" class="btn danger" disabled={busy || !!invalid}>
          {busy ? 'Stopping…' : 'Stop stream'}
        </button>
      </div>
    </form>
  </div>
</div>

<style>
  .backdrop {
    position: fixed; inset: 0; background: rgba(0,0,0,0.6);
    display: flex; align-items: center; justify-content: center; z-index: 100;
    padding: 1rem;
  }
  .modal {
    background: var(--bg-secondary, #1a1a1a); color: var(--text-primary, #eee);
    border: 1px solid var(--border); border-radius: 10px;
    padding: 1.25rem 1.4rem; max-width: 460px; width: 100%;
    box-shadow: 0 20px 60px rgba(0,0,0,0.5);
  }
  form { display: flex; flex-direction: column; gap: 0.55rem; }
  h2 { font-size: 1rem; font-weight: 600; margin: 0; }
  .summary { font-size: 0.85rem; margin: 0; color: var(--text-secondary); }
  .summary strong { color: var(--text-primary); }
  .hint { font-size: 0.75rem; margin: 0; color: var(--text-muted); line-height: 1.4; }
  .muted { color: var(--text-muted); }
  label { font-size: 0.75rem; color: var(--text-secondary); margin-top: 0.3rem; }
  textarea {
    width: 100%; box-sizing: border-box; resize: vertical;
    background: var(--bg-primary, #111); color: var(--text-primary);
    border: 1px solid var(--border); border-radius: 6px;
    padding: 0.5rem 0.6rem; font: inherit; font-size: 0.85rem;
  }
  textarea:focus { outline: none; border-color: var(--accent); }
  .counter { font-size: 0.68rem; color: var(--text-muted); text-align: right; }
  .counter.over { color: #f87171; }
  .err { font-size: 0.75rem; color: #f87171; margin: 0; }
  .actions { display: flex; justify-content: flex-end; gap: 0.5rem; margin-top: 0.4rem; }
  .btn {
    padding: 0.4rem 0.9rem; border-radius: 6px; font-size: 0.8rem; cursor: pointer;
    background: transparent; color: var(--text-secondary); border: 1px solid var(--border);
  }
  .btn:disabled { opacity: 0.6; cursor: default; }
  .btn.danger { color: #fff; background: #dc2626; border-color: #dc2626; }
  .btn.danger:hover:not(:disabled) { background: #b91c1c; }
</style>
