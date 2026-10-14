<script lang="ts">
  // Admin label chips on an item page: add / remove saves straight away
  // (PUT /items/{id}/labels). Labels drive per-user label restrictions, so
  // the hint says what they're for. Suggestions (labels already in use) load
  // the first time the box gets focus.
  import { labelApi } from '$lib/api';
  import LabelEditor from './LabelEditor.svelte';

  export let itemId: string;
  export let labels: string[] = [];

  let suggestions: string[] = [];
  let suggestionsLoaded = false;
  let saving = false;
  let error = '';

  async function loadSuggestions() {
    if (suggestionsLoaded) return;
    suggestionsLoaded = true;
    try {
      suggestions = (await labelApi.list()).map((l) => l.label);
    } catch {
      suggestions = [];
    }
  }

  async function save(next: string[]) {
    const prev = labels;
    labels = next;
    saving = true;
    error = '';
    try {
      labels = await labelApi.setItem(itemId, next);
      for (const l of labels) if (!suggestions.includes(l)) suggestions = [...suggestions, l].sort();
    } catch (e: unknown) {
      labels = prev;
      error = e instanceof Error ? e.message : 'Failed to save labels';
    } finally {
      saving = false;
    }
  }
</script>

<div class="item-labels">
  <span class="item-labels-title" id="item-labels-title-{itemId}">Labels</span>
  <div class="item-labels-editor">
    <LabelEditor
      {labels}
      {suggestions}
      disabled={saving}
      label="Labels"
      idSuffix={itemId}
      on:focus={loadSuggestions}
      on:change={(e) => save(e.detail.labels)}
    />
    <p class="hint">Visible to admins only. Users can be blocked from, or limited to, labelled titles in Settings ▸ Users.</p>
    {#if error}<p class="err" role="alert">{error}</p>{/if}
  </div>
</div>

<style>
  .item-labels { display: flex; align-items: flex-start; gap: 0.6rem; margin: 0.75rem 0; flex-wrap: wrap; }
  .item-labels-title {
    font-size: 0.72rem; font-weight: 600; text-transform: uppercase; letter-spacing: 0.04em;
    color: var(--text-muted); padding-top: 0.4rem;
  }
  .item-labels-editor { flex: 1; min-width: 14rem; max-width: 32rem; }
  .hint { font-size: 0.7rem; color: var(--text-muted); margin: 0.3rem 0 0; }
  .err { font-size: 0.72rem; color: #fca5a5; margin: 0.3rem 0 0; }
</style>
