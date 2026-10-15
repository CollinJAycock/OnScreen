<script lang="ts">
  // Admin "Edit metadata" dialog for movies, shows, seasons and episodes
  // (home videos and photos keep the simpler MetadataEditor). Loads the item
  // fresh on open, so the lock state is current, and saves through
  // PATCH /items/{id}: every field changed is locked against refreshes,
  // Fix Match and rescans; a locked field's padlock releases it on save.
  //
  // Native <dialog> + showModal(), like ReportProblemButton: it renders in
  // the top layer and closes on Escape. Controlled by the bindable `open`.
  import { itemApi, type ItemDetail, type MetadataEditResult } from '$lib/api';
  import {
    CONTENT_RATING_SUGGESTIONS,
    METADATA_FIELDS,
    buildMetadataEdit,
    changedFields,
    editErrorMessage,
    formFromItem,
    lockState,
    lockedFields,
    validateForm,
    type LockableField,
    type MetadataForm,
  } from '$lib/metadataEdit';

  interface Props {
    itemId: string;
    open?: boolean;
    /** Called with the saved values after a successful save. */
    onsaved?: (result: MetadataEditResult) => void;
  }

  let { itemId, open = $bindable(false), onsaved }: Props = $props();

  const uid = Math.random().toString(36).slice(2, 8);

  let dialogEl = $state<HTMLDialogElement>();
  let item = $state<ItemDetail | null>(null);
  let initial = $state<MetadataForm | null>(null);
  let form = $state<MetadataForm | null>(null);
  let locked = $state(new Set<LockableField>());
  let unlock = $state(new Set<LockableField>());
  let loading = $state(false);
  let saving = $state(false);
  let error = $state('');
  let loadSeq = 0;

  let changed = $derived(initial && form ? changedFields(initial, form) : []);
  let invalid = $derived(form ? validateForm(form) : '');
  let dirty = $derived(changed.length > 0 || unlock.size > 0);

  async function load() {
    const seq = ++loadSeq;
    loading = true;
    error = '';
    item = null;
    form = null;
    initial = null;
    unlock = new Set();
    try {
      const it = await itemApi.get(itemId);
      if (seq !== loadSeq) return;
      item = it;
      initial = formFromItem(it);
      form = { ...initial };
      locked = lockedFields(it);
    } catch (e) {
      if (seq === loadSeq) error = editErrorMessage(e);
    } finally {
      if (seq === loadSeq) loading = false;
    }
  }

  // Load each time the dialog opens; show it once it's rendered, and keep
  // typing inside it from reaching the host page (the player binds keys on
  // window).
  $effect(() => {
    if (open) void load();
    else loadSeq++;
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
    loadSeq++;
    open = false;
    if (dialogEl?.open) dialogEl.close();
  }

  function toggleUnlock(f: LockableField) {
    const next = new Set(unlock);
    if (next.has(f)) next.delete(f);
    else next.add(f);
    unlock = next;
  }

  function unlockAll() {
    unlock = new Set(locked);
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (!initial || !form || invalid || !dirty || saving) return;
    saving = true;
    error = '';
    try {
      const res = await itemApi.editMetadata(itemId, buildMetadataEdit(initial, form, unlock));
      onsaved?.(res);
      close();
    } catch (err) {
      error = editErrorMessage(err);
    } finally {
      saving = false;
    }
  }

  function lockTitle(state: string, label: string): string {
    switch (state) {
      case 'locked':
        return `${label} is locked: refreshes keep it. Click to unlock.`;
      case 'unlocking':
        return `${label} unlocks when you save; the next refresh can replace it. Click to keep it locked.`;
      case 'locking':
        return `${label} is edited and will be locked when you save.`;
      default:
        return `${label} is not locked: refreshes can replace it.`;
    }
  }

  function onBackdropClick(e: MouseEvent) {
    if (e.target === dialogEl) close();
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<dialog
  bind:this={dialogEl}
  class="me-dialog"
  aria-labelledby="me-title-{uid}"
  onclose={() => { loadSeq++; open = false; }}
  onclick={onBackdropClick}
>
  {#if open}
    <div class="me-body">
      <header class="me-head">
        <h2 id="me-title-{uid}">Edit metadata</h2>
        {#if locked.size > 0}
          <button type="button" class="me-link" onclick={unlockAll} disabled={unlock.size === locked.size}>
            Unlock all
          </button>
        {/if}
      </header>
      {#if item}<p class="me-subject">{item.title}</p>{/if}

      {#if loading}
        <p class="me-muted">Loading…</p>
      {:else if form}
        <p class="me-muted">Fields you change are locked, so refreshes and Fix Match keep your values.</p>
        <form onsubmit={save}>
          {#each METADATA_FIELDS as { field, label } (field)}
            {@const state = lockState(field, locked, unlock, changed)}
            <div class="me-row" class:wide={field === 'summary' || field === 'genres' || field === 'title'}>
              <div class="me-label-row">
                <label for="me-{field}-{uid}">{label}</label>
                <button
                  type="button"
                  class="me-lock {state}"
                  title={lockTitle(state, label)}
                  aria-label={lockTitle(state, label)}
                  aria-pressed={state === 'locked' || state === 'locking'}
                  disabled={state === 'open' || state === 'locking'}
                  onclick={() => toggleUnlock(field)}
                >
                  {#if state === 'locked' || state === 'locking'}
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="13" height="13" aria-hidden="true"><rect x="4" y="11" width="16" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/></svg>
                  {:else}
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="13" height="13" aria-hidden="true"><rect x="4" y="11" width="16" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 7.5-2"/></svg>
                  {/if}
                </button>
              </div>
              {#if field === 'summary'}
                <textarea id="me-{field}-{uid}" rows="5" bind:value={form.summary}></textarea>
              {:else if field === 'year'}
                <input id="me-{field}-{uid}" type="text" inputmode="numeric" maxlength="4" placeholder="YYYY" bind:value={form.year} />
              {:else if field === 'rating'}
                <input id="me-{field}-{uid}" type="text" inputmode="decimal" placeholder="0–10" bind:value={form.rating} />
              {:else if field === 'originally_available_at'}
                <input id="me-{field}-{uid}" type="date" bind:value={form.originally_available_at} />
              {:else if field === 'content_rating'}
                <input id="me-{field}-{uid}" type="text" maxlength="32" list="me-ratings-{uid}" bind:value={form.content_rating} />
              {:else if field === 'genres'}
                <input id="me-{field}-{uid}" type="text" placeholder="Drama, Thriller" bind:value={form.genres} />
              {:else if field === 'sort_title'}
                <input id="me-{field}-{uid}" type="text" maxlength="500" placeholder="Same as title" bind:value={form.sort_title} />
              {:else}
                <input id="me-{field}-{uid}" type="text" maxlength="500" bind:value={form[field]} />
              {/if}
            </div>
          {/each}
          <datalist id="me-ratings-{uid}">
            {#each CONTENT_RATING_SUGGESTIONS as r (r)}<option value={r}></option>{/each}
          </datalist>

          {#if error || invalid}<p class="me-error" role="alert">{error || invalid}</p>{/if}

          <div class="me-actions">
            <button type="button" class="me-secondary" onclick={close}>Cancel</button>
            <button type="submit" class="me-primary" disabled={!dirty || !!invalid || saving}>
              {saving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </form>
      {:else if error}
        <p class="me-error" role="alert">{error}</p>
        <div class="me-actions">
          <button type="button" class="me-secondary" onclick={close}>Close</button>
        </div>
      {/if}
    </div>
  {/if}
</dialog>

<style>
  .me-dialog {
    width: min(560px, calc(100vw - 2rem));
    max-height: calc(100vh - 2rem);
    padding: 0;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--bg-elevated);
    color: var(--text-primary);
    box-shadow: 0 12px 40px var(--shadow);
  }
  .me-dialog::backdrop {
    background: rgba(0, 0, 0, 0.55);
  }
  .me-body {
    padding: 1.1rem 1.2rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    text-align: left;
  }
  .me-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
  }
  h2 {
    margin: 0;
    font-size: 1.05rem;
    font-weight: 600;
  }
  .me-subject {
    margin: -0.35rem 0 0;
    font-size: 0.8rem;
    color: var(--text-muted);
  }
  .me-muted {
    margin: 0;
    font-size: 0.78rem;
    color: var(--text-muted);
  }
  .me-link {
    background: none;
    border: none;
    padding: 0;
    color: var(--accent);
    font-size: 0.78rem;
    cursor: pointer;
  }
  .me-link:disabled {
    color: var(--text-muted);
    cursor: default;
  }
  form {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.65rem 0.8rem;
  }
  .me-row {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
  }
  .me-row.wide,
  .me-error,
  .me-actions {
    grid-column: 1 / -1;
  }
  .me-label-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  label {
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
    font-weight: 600;
  }
  .me-lock {
    display: inline-flex;
    align-items: center;
    background: none;
    border: none;
    padding: 0.1rem 0.2rem;
    border-radius: 4px;
    color: var(--text-muted);
    opacity: 0.45;
    cursor: default;
  }
  .me-lock.locked {
    color: var(--accent);
    opacity: 1;
    cursor: pointer;
  }
  .me-lock.locked:hover {
    background: var(--bg-hover);
  }
  .me-lock.unlocking {
    color: var(--text-secondary);
    opacity: 1;
    cursor: pointer;
  }
  .me-lock.locking {
    color: var(--accent);
    opacity: 0.7;
  }
  input,
  textarea {
    background: var(--input-bg, var(--bg-hover));
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.45rem 0.6rem;
    color: var(--text-primary);
    font: inherit;
    font-size: 0.85rem;
    min-width: 0;
  }
  textarea {
    resize: vertical;
    min-height: 90px;
  }
  input:focus,
  textarea:focus {
    outline: none;
    border-color: var(--accent);
  }
  .me-error {
    margin: 0;
    font-size: 0.8rem;
    color: #f87171;
  }
  .me-actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
    margin-top: 0.2rem;
  }
  .me-primary,
  .me-secondary {
    padding: 0.45rem 1rem;
    border-radius: 7px;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    border: 1px solid transparent;
  }
  .me-secondary {
    background: var(--bg-hover);
    color: var(--text-secondary);
    border-color: var(--border-strong);
  }
  .me-primary {
    background: var(--accent);
    color: #fff;
  }
  .me-primary:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  @media (max-width: 520px) {
    form {
      grid-template-columns: 1fr;
    }
  }
</style>
