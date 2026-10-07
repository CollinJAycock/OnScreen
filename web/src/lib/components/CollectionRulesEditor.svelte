<script lang="ts">
  // The smart-collection rules form. The parent owns saving: it gets the
  // validated rules from `save` (or the error message to show).
  import { createEventDispatcher, onMount } from 'svelte';
  import { libraryApi, type CollectionRules, type Library } from '$lib/api';
  import { formToRules, rulesToForm, type RulesForm } from '$lib/collectionRules';

  export let rules: CollectionRules | null | undefined = undefined;
  export let busy = false;
  export let submitLabel = 'Save rules';

  const dispatch = createEventDispatcher<{ save: CollectionRules; cancel: void }>();
  let form: RulesForm = rulesToForm(rules);
  let error = '';
  let libraries: Library[] = [];

  onMount(async () => {
    try {
      libraries = (await libraryApi.list()).filter((l) => l.type === 'movie' || l.type === 'show' || l.type === 'anime' || l.type === 'cartoons');
    } catch { /* library limit just isn't offered */ }
  });

  function toggleLibrary(id: string, on: boolean) {
    form.libraryIds = on ? [...form.libraryIds, id] : form.libraryIds.filter((x) => x !== id);
  }

  function submit() {
    try {
      error = '';
      dispatch('save', formToRules(form));
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Invalid rules';
    }
  }
</script>

<form class="rules" on:submit|preventDefault={submit} aria-label="Smart collection rules">
  <fieldset class="row">
    <legend>Include</legend>
    <label><input type="checkbox" bind:checked={form.movies} /> Movies</label>
    <label><input type="checkbox" bind:checked={form.shows} /> Shows</label>
  </fieldset>
  <div class="row fields">
    <label>Genre <input bind:value={form.genre} placeholder="Any" /></label>
    <label>From year <input bind:value={form.yearMin} inputmode="numeric" placeholder="Any" /></label>
    <label>To year <input bind:value={form.yearMax} inputmode="numeric" placeholder="Any" /></label>
    <label>Min rating <input bind:value={form.ratingMin} inputmode="decimal" placeholder="0–10" /></label>
    <label>Limit <input bind:value={form.limit} inputmode="numeric" placeholder="200" /></label>
  </div>
  {#if libraries.length > 1}
    <fieldset class="row">
      <legend>Libraries <span class="muted">(none ticked = all)</span></legend>
      {#each libraries as lib (lib.id)}
        <label>
          <input type="checkbox" checked={form.libraryIds.includes(lib.id)}
                 on:change={(e) => toggleLibrary(lib.id, e.currentTarget.checked)} />
          {lib.name}
        </label>
      {/each}
    </fieldset>
  {/if}
  {#if error}<div class="err" role="alert">{error}</div>{/if}
  <div class="row actions">
    <button type="submit" class="btn-save" disabled={busy}>{submitLabel}</button>
    <button type="button" class="btn-cancel" on:click={() => dispatch('cancel')}>Cancel</button>
  </div>
</form>

<style>
  .rules {
    display: flex; flex-direction: column; gap: 0.6rem; padding: 0.9rem 1rem; margin-bottom: 1.5rem;
    background: var(--input-bg); border: 1px solid var(--border); border-radius: 8px; max-width: 760px;
  }
  .row { display: flex; flex-wrap: wrap; gap: 0.5rem 1rem; align-items: center; border: none; padding: 0; margin: 0; }
  legend { font-size: 0.72rem; color: var(--text-muted); margin-bottom: 0.3rem; padding: 0; }
  label { font-size: 0.75rem; color: var(--text-secondary); display: flex; align-items: center; gap: 0.35rem; }
  .fields input {
    width: 6.5rem; background: var(--bg-hover); border: 1px solid var(--border-strong); border-radius: 6px;
    color: var(--text-primary); font-size: 0.75rem; padding: 0.25rem 0.45rem;
  }
  .fields input:focus { outline: none; border-color: var(--accent); }
  .muted { color: var(--text-muted); }
  .err { font-size: 0.75rem; color: var(--error); }
  .btn-save {
    padding: 0.35rem 0.7rem; background: var(--accent); border: none; border-radius: 6px;
    color: #fff; font-size: 0.75rem; font-weight: 600; cursor: pointer;
  }
  .btn-save:disabled { opacity: 0.5; cursor: default; }
  .btn-cancel {
    padding: 0.35rem 0.7rem; background: var(--bg-hover); border: 1px solid var(--border-strong);
    border-radius: 6px; color: var(--text-muted); font-size: 0.75rem; cursor: pointer;
  }
</style>
