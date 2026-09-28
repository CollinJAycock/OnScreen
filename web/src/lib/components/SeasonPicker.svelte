<script lang="ts">
  // Season picker for a TV request: every season of the show with what the
  // user already has ("In library"), what they've already asked for
  // ("Requested · Pending"), and checkboxes for the rest. Specials come last
  // and start unticked. The parent owns the actual request (its quota /
  // can-request / over-quota handling) through onsubmit; this component only
  // chooses the seasons.
  //
  // Render it when the picker should be open ({#if …}<SeasonPicker …/>) — it
  // opens itself as a native modal <dialog> and calls onclose when dismissed.
  import { discoverApi, type SeasonInfo } from '$lib/api';
  import {
    allState,
    defaultSelection,
    isSelectable,
    requestSeasons,
    seasonNote,
    seasonState,
    seasonTitle,
    submitLabel,
    toggleAll,
  } from '$lib/seasonPicker';

  interface Props {
    tmdbId: number;
    title: string;
    /** Called with the seasons to request (undefined = every season).
     *  Resolves null when the request was made (the picker then closes), or
     *  an error message to show in the picker. */
    onsubmit: (seasons: number[] | undefined) => Promise<string | null>;
    onclose: () => void;
  }

  let { tmdbId, title, onsubmit, onclose }: Props = $props();

  const uid = Math.random().toString(36).slice(2, 8);

  let dialogEl = $state<HTMLDialogElement>();
  let seasons = $state<SeasonInfo[]>([]);
  let loading = $state(true);
  let loadError = $state('');
  let selected = $state<Set<number>>(new Set());
  let submitting = $state(false);
  let error = $state('');
  let closed = false;

  let payload = $derived(requestSeasons(seasons, selected));
  let all = $derived(allState(seasons, selected));
  let anyPickable = $derived(seasons.some((s) => s.season_number > 0 && isSelectable(s)));
  let canSubmit = $derived(!submitting && !loading && (payload === undefined || payload.length > 0));

  async function load(id: number) {
    loading = true;
    loadError = '';
    try {
      const list = (await discoverApi.seasons(id)) ?? [];
      seasons = list;
      selected = defaultSelection(list);
    } catch (e) {
      seasons = [];
      loadError = e instanceof Error && e.message ? e.message : "Couldn't load the seasons";
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    void load(tmdbId);
  });

  // Open as a modal once rendered; keep keys inside from reaching the page
  // (the player binds shortcuts on window).
  $effect(() => {
    const el = dialogEl;
    if (!el) return;
    if (!el.open) {
      if (typeof el.showModal === 'function') el.showModal();
      else el.setAttribute('open', '');
    }
    const stop = (e: Event) => e.stopPropagation();
    el.addEventListener('keydown', stop);
    return () => el.removeEventListener('keydown', stop);
  });

  function close() {
    if (closed) return;
    closed = true;
    if (dialogEl?.open && typeof dialogEl.close === 'function') dialogEl.close();
    onclose();
  }

  function toggle(n: number) {
    const next = new Set(selected);
    if (next.has(n)) next.delete(n);
    else next.add(n);
    selected = next;
  }

  async function send(seasonsToSend: number[] | undefined) {
    submitting = true;
    error = '';
    try {
      const msg = await onsubmit(seasonsToSend);
      if (msg) error = msg;
      else close();
    } catch (e) {
      error = e instanceof Error ? e.message : 'Request failed';
    } finally {
      submitting = false;
    }
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    void send(payload);
  }

  function onBackdropClick(e: MouseEvent) {
    if (e.target === dialogEl) close();
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
<dialog
  bind:this={dialogEl}
  class="sp-dialog"
  aria-labelledby="sp-title-{uid}"
  onclose={close}
  onclick={onBackdropClick}
>
  <div class="sp-body">
    <h2 id="sp-title-{uid}">Choose seasons</h2>
    <p class="sp-subject">{title}</p>

    {#if loading}
      <p class="sp-muted">Loading seasons…</p>
    {:else if loadError}
      <p class="sp-error" role="alert">{loadError}</p>
      {#if error}<p class="sp-error" role="alert">{error}</p>{/if}
      <div class="sp-actions">
        <button type="button" class="sp-secondary" onclick={close}>Cancel</button>
        <button type="button" class="sp-primary" disabled={submitting} onclick={() => send(undefined)}>
          {submitting ? 'Sending…' : 'Request all seasons'}
        </button>
      </div>
    {:else}
      <form onsubmit={submit}>
        <label class="sp-all">
          <input
            type="checkbox"
            checked={all === 'all'}
            indeterminate={all === 'some'}
            disabled={!anyPickable}
            onchange={() => (selected = toggleAll(seasons, selected))}
          />
          <span>All seasons</span>
        </label>
        <ul class="sp-list" aria-label="Seasons">
          {#each seasons as s (s.season_number)}
            {@const st = seasonState(s)}
            {@const pickable = isSelectable(s)}
            <li class="sp-row {st}">
              <label>
                <input
                  type="checkbox"
                  checked={pickable ? selected.has(s.season_number) : true}
                  disabled={!pickable}
                  onchange={() => toggle(s.season_number)}
                />
                <span class="sp-name">{seasonTitle(s)}</span>
                <span class="sp-note">{seasonNote(s)}</span>
              </label>
            </li>
          {/each}
        </ul>
        {#if !anyPickable && !seasons.some((s) => s.season_number === 0 && isSelectable(s))}
          <p class="sp-muted">You already have or have requested every season.</p>
        {/if}
        {#if error}<p class="sp-error" role="alert">{error}</p>{/if}
        <div class="sp-actions">
          <button type="button" class="sp-secondary" onclick={close}>Cancel</button>
          <button type="submit" class="sp-primary" disabled={!canSubmit}>
            {submitting ? 'Sending…' : submitLabel(payload)}
          </button>
        </div>
      </form>
    {/if}
  </div>
</dialog>

<style>
  .sp-dialog {
    width: min(460px, calc(100vw - 2rem));
    max-height: calc(100vh - 2rem);
    padding: 0;
    border: 1px solid var(--border, rgba(255, 255, 255, 0.12));
    border-radius: 10px;
    background: var(--bg-elevated, var(--bg-secondary, #16161f));
    color: var(--text-primary, #eee);
    box-shadow: 0 12px 40px rgba(0, 0, 0, 0.45);
  }
  .sp-dialog::backdrop {
    background: rgba(0, 0, 0, 0.55);
  }
  .sp-body {
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
  .sp-subject {
    margin: -0.35rem 0 0;
    font-size: 0.8rem;
    color: var(--text-muted, #999);
  }
  .sp-muted {
    margin: 0;
    font-size: 0.78rem;
    color: var(--text-muted, #999);
  }
  form {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .sp-all {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
    font-weight: 600;
    padding: 0.3rem 0.4rem;
    border-bottom: 1px solid var(--border, rgba(255, 255, 255, 0.08));
    cursor: pointer;
  }
  .sp-list {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 50vh;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 0.1rem;
  }
  .sp-row label {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
    padding: 0.3rem 0.4rem;
    border-radius: 5px;
    cursor: pointer;
  }
  .sp-row label:hover {
    background: var(--bg-hover, rgba(255, 255, 255, 0.06));
  }
  .sp-row.owned label,
  .sp-row.requested label {
    cursor: default;
    opacity: 0.65;
  }
  .sp-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .sp-note {
    font-size: 0.72rem;
    color: var(--text-muted, #999);
    white-space: nowrap;
  }
  .sp-row.owned .sp-note {
    color: var(--success, #6ee7b7);
  }
  .sp-row.requested .sp-note {
    color: var(--accent-text, #a5b4fc);
  }
  .sp-error {
    margin: 0;
    font-size: 0.8rem;
    color: #f87171;
  }
  .sp-actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
    margin-top: 0.2rem;
  }
  .sp-primary,
  .sp-secondary {
    border-radius: 6px;
    font-size: 0.8rem;
    font-weight: 600;
    padding: 0.4rem 0.8rem;
    cursor: pointer;
  }
  .sp-primary {
    background: var(--accent, #7c6af7);
    color: #fff;
    border: none;
  }
  .sp-primary:hover:not(:disabled) {
    background: var(--accent-hover, #6b59e6);
  }
  .sp-primary:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .sp-secondary {
    background: transparent;
    color: var(--text-secondary, #ccc);
    border: 1px solid var(--border-strong, rgba(255, 255, 255, 0.2));
  }
</style>
