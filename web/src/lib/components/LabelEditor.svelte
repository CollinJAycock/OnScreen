<script lang="ts">
  // Chip editor for a list of admin labels: each label is a removable chip,
  // and a text box (with the labels already in use as suggestions) adds
  // more on Enter or comma. Purely presentational — the parent saves on
  // `change`. Labels are shown as the server stores them (normalizeLabel).
  import { createEventDispatcher } from 'svelte';
  import { normalizeLabel } from '$lib/api';

  export let labels: string[] = [];
  export let suggestions: string[] = [];
  export let disabled = false;
  export let placeholder = 'Add label…';
  /** Accessible name for the text box and chip list. */
  export let label = 'Labels';
  /** Suffix keeping the <datalist> id unique when several editors share a page. */
  export let idSuffix = '';

  const dispatch = createEventDispatcher<{ change: { labels: string[] }; focus: void }>();

  let draft = '';
  $: listId = `label-suggestions${idSuffix ? '-' + idSuffix : ''}`;
  $: offered = suggestions.filter((s) => !labels.includes(s));

  function add(raw: string) {
    const next = [...labels];
    for (const part of raw.split(',')) {
      const l = normalizeLabel(part);
      if (l && !next.includes(l)) next.push(l);
    }
    draft = '';
    if (next.length !== labels.length) {
      next.sort();
      dispatch('change', { labels: next });
    }
  }

  function remove(l: string) {
    dispatch('change', { labels: labels.filter((x) => x !== l) });
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault();
      add(draft);
    } else if (e.key === 'Backspace' && draft === '' && labels.length > 0) {
      remove(labels[labels.length - 1]);
    }
  }
</script>

<div class="label-editor" class:disabled>
  <ul class="chips" aria-label={label}>
    {#each labels as l (l)}
      <li class="chip">
        <span>{l}</span>
        <button type="button" class="chip-x" aria-label="Remove label {l}" {disabled} on:click={() => remove(l)}>×</button>
      </li>
    {/each}
  </ul>
  <input
    type="text"
    bind:value={draft}
    list={listId}
    {placeholder}
    {disabled}
    aria-label="{label}: add"
    maxlength="64"
    on:keydown={onKeydown}
    on:focus={() => dispatch('focus')}
    on:change={() => { if (draft.trim() && offered.includes(normalizeLabel(draft))) add(draft); }}
  />
  <datalist id={listId}>
    {#each offered as s (s)}
      <option value={s}></option>
    {/each}
  </datalist>
</div>

<style>
  .label-editor {
    display: flex; flex-wrap: wrap; align-items: center; gap: 0.3rem;
    padding: 0.3rem 0.4rem; background: var(--input-bg);
    border: 1px solid var(--border-strong); border-radius: 7px;
  }
  .label-editor:focus-within { border-color: var(--accent); }
  .label-editor.disabled { opacity: 0.6; }
  .chips { display: contents; list-style: none; margin: 0; padding: 0; }
  .chip {
    display: inline-flex; align-items: center; gap: 0.2rem;
    padding: 0.12rem 0.25rem 0.12rem 0.5rem; border-radius: 999px;
    background: rgba(124,106,247,0.14); color: var(--accent-text);
    font-size: 0.74rem; line-height: 1.3;
  }
  .chip-x {
    background: none; border: none; color: inherit; cursor: pointer;
    font-size: 0.85rem; line-height: 1; padding: 0 0.15rem; opacity: 0.7;
  }
  .chip-x:hover { opacity: 1; }
  input {
    flex: 1; min-width: 7rem; background: none; border: none; outline: none;
    color: var(--text-primary); font-size: 0.8rem; padding: 0.15rem 0.2rem;
  }
</style>
