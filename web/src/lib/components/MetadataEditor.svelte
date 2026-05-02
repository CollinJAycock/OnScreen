<script lang="ts">
  // MetadataEditor — admin-only modal for the three fields users
  // actually need to manage on items the metadata agent doesn't enrich
  // (home_video + photo): displayed title, free-form summary, and the
  // date the date-grouped grid (home videos) / EXIF timeline (photos)
  // sorts on. Wraps PATCH /api/v1/items/{id}; on success dispatches
  // `saved` with the updated values so the caller can reflect them
  // without a full reload.
  //
  // Date input emits YYYY-MM-DD; the server normalises to midnight UTC
  // for date-only payloads.

  import { createEventDispatcher } from 'svelte';
  import { itemApi } from '$lib/api';

  export let itemId: string;
  export let initialTitle = '';
  export let initialSummary: string | null | undefined = '';
  export let initialTakenAt: string | null | undefined = ''; // ISO timestamp from the server
  export let open = false;

  const dispatch = createEventDispatcher<{
    close: void;
    saved: { title: string; summary: string | null; taken_at: string | null };
  }>();

  let title = '';
  let summary = '';
  let takenAt = ''; // YYYY-MM-DD form value
  let saving = false;
  let error = '';

  // Reset form fields whenever the modal opens. The reactive trigger
  // is `open` flipping false→true, so a parent that re-uses the same
  // <MetadataEditor> across multiple items always sees fresh values.
  $: if (open) reset();

  function reset() {
    title = initialTitle;
    summary = (initialSummary ?? '') + '';
    takenAt = initialTakenAt ? initialTakenAt.slice(0, 10) : '';
    error = '';
    saving = false;
  }

  async function save() {
    if (!title.trim()) {
      error = 'Title is required';
      return;
    }
    saving = true;
    error = '';
    try {
      const res = await itemApi.updateMetadata(itemId, {
        title: title.trim(),
        summary,                   // empty string clears
        taken_at: takenAt || '',   // empty string clears
      });
      dispatch('saved', {
        title: res.title,
        summary: res.summary,
        taken_at: res.originally_available_at,
      });
      dispatch('close');
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Save failed';
    } finally {
      saving = false;
    }
  }

  function onBackdropKey(e: KeyboardEvent) {
    if (e.key === 'Escape') dispatch('close');
  }
</script>

{#if open}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="backdrop" on:click={() => dispatch('close')} on:keydown={onBackdropKey}>
    <div class="panel" on:click|stopPropagation role="dialog" aria-modal="true" aria-label="Edit metadata">
      <header class="panel-header">
        <span>Edit metadata</span>
        <button class="close-btn" on:click={() => dispatch('close')} aria-label="Close">×</button>
      </header>

      {#if error}<div class="msg error">{error}</div>{/if}

      <form class="form" on:submit|preventDefault={save}>
        <label>
          <span class="lbl">Title</span>
          <input type="text" bind:value={title} required maxlength="500" />
        </label>

        <label>
          <span class="lbl">Summary</span>
          <textarea bind:value={summary} rows="4" placeholder="Optional notes about this item"></textarea>
        </label>

        <label>
          <span class="lbl">Date</span>
          <input type="date" bind:value={takenAt} />
          <span class="hint">When this was recorded — drives the date-grouped grid sort.</span>
        </label>

        <div class="actions">
          <button type="button" class="btn-cancel" on:click={() => dispatch('close')} disabled={saving}>Cancel</button>
          <button type="submit" class="btn-save" disabled={saving || !title.trim()}>
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>
    </div>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed; inset: 0; background: var(--shadow);
    display: flex; align-items: center; justify-content: center;
    z-index: 2000; animation: fadeIn 0.1s ease-out;
  }
  @keyframes fadeIn { from { opacity: 0; } to { opacity: 1; } }

  .panel {
    background: var(--bg-elevated); border: 1px solid var(--border);
    border-radius: 12px; width: 460px; max-width: calc(100vw - 2rem);
    box-shadow: 0 20px 60px var(--shadow);
  }
  .panel-header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 0.85rem 1rem; border-bottom: 1px solid var(--border);
    font-size: 0.88rem; font-weight: 600; color: var(--text-primary);
  }
  .close-btn {
    background: none; border: none; color: var(--text-muted);
    font-size: 1.2rem; cursor: pointer; padding: 0 0.25rem; line-height: 1;
  }
  .close-btn:hover { color: var(--text-secondary); }

  .msg {
    padding: 0.55rem 1rem; font-size: 0.75rem;
  }
  .msg.error { color: #fca5a5; background: rgba(248,113,113,0.08); }

  .form { padding: 0.9rem 1rem 1rem; display: flex; flex-direction: column; gap: 0.85rem; }
  label { display: flex; flex-direction: column; gap: 0.3rem; }
  .lbl {
    font-size: 0.7rem; text-transform: uppercase; letter-spacing: 0.05em;
    color: var(--text-muted); font-weight: 600;
  }
  input[type="text"], input[type="date"], textarea {
    background: var(--input-bg, var(--bg-hover));
    border: 1px solid var(--border-strong);
    border-radius: 7px; padding: 0.5rem 0.7rem;
    color: var(--text-primary); font-size: 0.85rem;
    font-family: inherit;
  }
  textarea { resize: vertical; min-height: 80px; }
  input:focus, textarea:focus { outline: none; border-color: var(--accent); }
  .hint { font-size: 0.7rem; color: var(--text-muted); margin-top: 0.1rem; }

  .actions {
    display: flex; justify-content: flex-end; gap: 0.5rem; margin-top: 0.4rem;
  }
  .btn-cancel, .btn-save {
    padding: 0.45rem 1rem; border-radius: 7px; font-size: 0.8rem;
    font-weight: 600; cursor: pointer; border: 1px solid transparent;
  }
  .btn-cancel {
    background: var(--bg-hover); color: var(--text-secondary);
    border-color: var(--border-strong);
  }
  .btn-cancel:hover:not(:disabled) { color: var(--text-primary); }
  .btn-save {
    background: var(--accent); color: #fff;
  }
  .btn-save:hover:not(:disabled) { filter: brightness(1.1); }
  .btn-save:disabled, .btn-cancel:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
