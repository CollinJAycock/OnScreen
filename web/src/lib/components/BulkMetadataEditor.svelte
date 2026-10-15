<script lang="ts">
  // Admin "Edit metadata…" for a library selection: one edit applied to
  // every selected item through PATCH /items/bulk, in a single transaction.
  // Every field starts at "leave unchanged". As in the single-item editor,
  // fields written are locked; an item's locked field is skipped (and listed
  // in the result) unless the dialog unlocks it too.
  //
  // Native <dialog> + showModal(), like ItemMetadataEditor.
  import { itemApi, labelApi, type BulkEditResult } from '$lib/api';
  import { CONTENT_RATING_SUGGESTIONS, METADATA_FIELDS, editErrorMessage, type LockableField } from '$lib/metadataEdit';
  import {
    MAX_BULK_ITEMS,
    applicableCount,
    buildBulkRequest,
    emptyBulkForm,
    skipReasonText,
    summarizeBulkResult,
    validateBulkForm,
    type BulkEditForm,
    type GenreMode,
    type LockChoice,
    type RatingMode,
  } from '$lib/bulkEdit';
  import LabelEditor from './LabelEditor.svelte';

  interface Props {
    /** The selection: ids with their type and title (for the result list). */
    items: { id: string; type: string; title: string }[];
    open?: boolean;
    /** Called after a successful apply. */
    onapplied?: (result: BulkEditResult) => void;
  }

  let { items, open = $bindable(false), onapplied }: Props = $props();

  const uid = Math.random().toString(36).slice(2, 8);

  let dialogEl = $state<HTMLDialogElement>();
  let form = $state<BulkEditForm>(emptyBulkForm());
  let labelsInUse = $state<string[]>([]);
  let saving = $state(false);
  let error = $state('');
  let result = $state<BulkEditResult | null>(null);

  let types = $derived(items.map((i) => i.type));
  let ratingApplies = $derived(applicableCount('content_rating', types));
  let genreApplies = $derived(applicableCount('genres', types));
  let invalid = $derived(validateBulkForm(form));
  let request = $derived(buildBulkRequest(items.map((i) => i.id), form));
  let tooMany = $derived(items.length > MAX_BULK_ITEMS);
  let titles = $derived(new Map(items.map((i) => [i.id, i.title])));
  let fieldLabels = $derived(new Map(METADATA_FIELDS.map((f) => [f.field as string, f.label])));

  // Reset each time it opens; show it once rendered, and keep typing inside
  // it from reaching the host page.
  $effect(() => {
    if (!open) return;
    form = emptyBulkForm();
    error = '';
    result = null;
    labelApi.list().then((l) => { labelsInUse = l.map((x) => x.label); }).catch(() => { labelsInUse = []; });
  });
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

  function close() {
    open = false;
    if (dialogEl?.open) dialogEl.close();
  }

  function setLock(field: LockableField, choice: LockChoice) {
    form.locks = { ...form.locks, [field]: choice };
  }

  async function apply(e: SubmitEvent) {
    e.preventDefault();
    if (!request || invalid || saving || tooMany) return;
    saving = true;
    error = '';
    try {
      result = await itemApi.bulkEdit(request);
      onapplied?.(result);
    } catch (err) {
      error = editErrorMessage(err);
    } finally {
      saving = false;
    }
  }

  function onBackdropClick(e: MouseEvent) {
    if (e.target === dialogEl) close();
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<dialog
  bind:this={dialogEl}
  class="be-dialog"
  aria-labelledby="be-title-{uid}"
  onclose={() => { open = false; }}
  onclick={onBackdropClick}
>
  {#if open}
    <div class="be-body">
      <h2 id="be-title-{uid}">Edit {items.length} item{items.length === 1 ? '' : 's'}</h2>

      {#if result}
        <p class="be-summary" role="status">{summarizeBulkResult(result, items.length)}</p>
        {#if result.skipped.length > 0}
          <details class="be-skipped">
            <summary>Skipped ({result.skipped.length})</summary>
            <ul>
              {#each result.skipped.slice(0, 200) as s (s.item_id + (s.field ?? ''))}
                <li>
                  <span class="be-skip-title">{s.title || titles.get(s.item_id) || s.item_id}</span>
                  {#if s.field}<span class="be-muted"> · {fieldLabels.get(s.field) ?? s.field}</span>{/if}
                  <span class="be-muted"> — {skipReasonText(s.reason)}</span>
                </li>
              {/each}
            </ul>
            {#if result.skipped.some((s) => s.reason === 'locked')}
              <p class="be-muted">Choose “Unlock” for a field below and apply again to overwrite locked values.</p>
            {/if}
          </details>
        {/if}
        <div class="be-actions">
          <button type="button" class="be-secondary" onclick={() => { result = null; }}>Edit again</button>
          <button type="button" class="be-primary" onclick={close}>Done</button>
        </div>
      {:else}
        <p class="be-muted">
          Only the fields you change are written; each is locked so refreshes keep it.
          Fields an item has locked are skipped unless you unlock them below.
        </p>
        <form onsubmit={apply}>
          <fieldset class="be-field">
            <legend>Content rating</legend>
            <div class="be-inline">
              <select class="be-select" aria-label="Content rating change" value={form.ratingMode}
                onchange={(e) => { form.ratingMode = (e.currentTarget as HTMLSelectElement).value as RatingMode; }}>
                <option value="keep">Leave unchanged</option>
                <option value="set">Set to…</option>
                <option value="clear">Clear</option>
              </select>
              {#if form.ratingMode === 'set'}
                <input class="be-input" type="text" list="be-ratings-{uid}" aria-label="Content rating"
                  placeholder="PG-13" maxlength="32" bind:value={form.rating} />
                <datalist id="be-ratings-{uid}">
                  {#each CONTENT_RATING_SUGGESTIONS as r}<option value={r}></option>{/each}
                </datalist>
              {/if}
            </div>
            {#if form.ratingMode !== 'keep' && ratingApplies < items.length}
              <p class="be-hint">Applies to {ratingApplies} of {items.length}; seasons, tracks, photos and the like don't carry their own rating.</p>
            {/if}
          </fieldset>

          <fieldset class="be-field">
            <legend>Genres</legend>
            <div class="be-inline">
              <select class="be-select" aria-label="Genre change" value={form.genreMode}
                onchange={(e) => { form.genreMode = (e.currentTarget as HTMLSelectElement).value as GenreMode; }}>
                <option value="keep">Leave unchanged</option>
                <option value="add">Add</option>
                <option value="remove">Remove</option>
                <option value="replace">Replace with</option>
              </select>
              {#if form.genreMode !== 'keep'}
                <input class="be-input" type="text" aria-label="Genres, comma-separated"
                  placeholder={form.genreMode === 'replace' ? 'Drama, Thriller (empty clears)' : 'Drama, Thriller'}
                  bind:value={form.genres} />
              {/if}
            </div>
            {#if form.genreMode !== 'keep' && genreApplies < items.length}
              <p class="be-hint">Applies to {genreApplies} of {items.length} selected items.</p>
            {/if}
          </fieldset>

          <fieldset class="be-field">
            <legend>Labels</legend>
            <div class="be-labels">
              <LabelEditor
                labels={form.addLabels}
                suggestions={labelsInUse}
                disabled={saving}
                label="Labels to add"
                placeholder="Add label…"
                idSuffix="bulk-add-{uid}"
                on:change={(e) => { form.addLabels = e.detail.labels; }}
              />
              <LabelEditor
                labels={form.removeLabels}
                suggestions={labelsInUse}
                disabled={saving}
                label="Labels to remove"
                placeholder="Remove label…"
                idSuffix="bulk-remove-{uid}"
                on:change={(e) => { form.removeLabels = e.detail.labels; }}
              />
            </div>
          </fieldset>

          <fieldset class="be-field">
            <legend>Field locks</legend>
            <p class="be-hint">Lock keeps a field's current value through refreshes and Fix Match; unlock hands it back to them.</p>
            <div class="be-locks">
              {#each METADATA_FIELDS as { field, label } (field)}
                <div class="be-lock-row">
                  <span aria-hidden="true">{label}</span>
                  <select id="be-lock-{field}-{uid}" class="be-select" aria-label="{label} lock"
                    value={form.locks[field] ?? 'keep'}
                    onchange={(e) => setLock(field, (e.currentTarget as HTMLSelectElement).value as LockChoice)}>
                    <option value="keep">Leave unchanged</option>
                    <option value="lock">Lock</option>
                    <option value="unlock">Unlock</option>
                  </select>
                </div>
              {/each}
            </div>
          </fieldset>

          {#if tooMany}
            <p class="be-error" role="alert">Select at most {MAX_BULK_ITEMS} items at a time.</p>
          {:else if invalid}
            <p class="be-error" role="alert">{invalid}</p>
          {/if}
          {#if error}<p class="be-error" role="alert">{error}</p>{/if}

          <div class="be-actions">
            <button type="button" class="be-secondary" onclick={close}>Cancel</button>
            <button type="submit" class="be-primary" disabled={!request || !!invalid || saving || tooMany}>
              {saving ? 'Applying…' : `Apply to ${items.length}`}
            </button>
          </div>
        </form>
      {/if}
    </div>
  {/if}
</dialog>

<style>
  .be-dialog {
    width: min(560px, calc(100vw - 2rem));
    max-height: calc(100vh - 2rem);
    padding: 0;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--bg-elevated);
    color: var(--text-primary);
    box-shadow: 0 12px 40px var(--shadow);
  }
  .be-dialog::backdrop { background: rgba(0, 0, 0, 0.55); }
  .be-body {
    padding: 1.1rem 1.2rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.7rem;
    text-align: left;
  }
  h2 { margin: 0; font-size: 1.05rem; font-weight: 600; }
  .be-muted, .be-hint { margin: 0; font-size: 0.78rem; color: var(--text-muted); }
  .be-hint { font-size: 0.72rem; margin-top: 0.35rem; }
  form { display: flex; flex-direction: column; gap: 0.8rem; }
  .be-field {
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.55rem 0.7rem 0.7rem;
    margin: 0;
    min-width: 0;
  }
  legend {
    padding: 0 0.3rem;
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
    font-weight: 600;
  }
  .be-inline { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .be-select, .be-input {
    background: var(--input-bg, var(--bg-hover));
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.4rem 0.55rem;
    color: var(--text-primary);
    font: inherit;
    font-size: 0.83rem;
    min-width: 0;
  }
  .be-select option { background: var(--bg-elevated); color: var(--text-primary); }
  .be-input { flex: 1 1 180px; }
  .be-select:focus, .be-input:focus { outline: none; border-color: var(--accent); }
  .be-labels { display: flex; flex-direction: column; gap: 0.5rem; }
  .be-locks {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.35rem 0.9rem;
    margin-top: 0.45rem;
  }
  .be-lock-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    font-size: 0.8rem;
    color: var(--text-secondary);
  }
  .be-lock-row .be-select { font-size: 0.75rem; padding: 0.25rem 0.4rem; }
  .be-error { margin: 0; font-size: 0.8rem; color: #f87171; }
  .be-summary { margin: 0; font-size: 0.85rem; color: var(--text-primary); }
  .be-skipped { font-size: 0.78rem; color: var(--text-secondary); }
  .be-skipped summary { cursor: pointer; }
  .be-skipped ul {
    margin: 0.4rem 0;
    padding-left: 1.1rem;
    max-height: 220px;
    overflow-y: auto;
  }
  .be-skip-title { color: var(--text-primary); }
  .be-actions { display: flex; justify-content: flex-end; gap: 0.5rem; margin-top: 0.2rem; }
  .be-primary, .be-secondary {
    padding: 0.45rem 1rem;
    border-radius: 7px;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    border: 1px solid transparent;
  }
  .be-secondary { background: var(--bg-hover); color: var(--text-secondary); border-color: var(--border-strong); }
  .be-primary { background: var(--accent); color: #fff; }
  .be-primary:disabled { opacity: 0.5; cursor: not-allowed; }
  @media (max-width: 520px) {
    .be-locks { grid-template-columns: 1fr; }
  }
</style>
