<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, type SearchResult, Unauthorized } from '$lib/api';
  import OnScreenKeyboard from '$lib/components/OnScreenKeyboard.svelte';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import { focusManager } from '$lib/focus/manager';

  let query = $state('');
  let results = $state<SearchResult[]>([]);
  let searching = $state(false);

  let debounce: ReturnType<typeof setTimeout> | null = null;

  function onQueryChange(v: string) {
    query = v;
    if (debounce) clearTimeout(debounce);
    if (v.trim().length < 2) {
      results = [];
      return;
    }
    debounce = setTimeout(async () => {
      searching = true;
      try {
        results = await endpoints.search.query(v.trim(), 24);
      } catch (e) {
        if (e instanceof Unauthorized) goto('/login');
      } finally {
        searching = false;
      }
    }, 250);
  }

  onMount(() => {
    const off = focusManager.pushBack(() => {
      goto('/hub');
      return true;
    });
    return off;
  });
</script>

<div class="page">
  <h1>Search</h1>

  <OnScreenKeyboard value={query} onchange={onQueryChange} />

  <section class="results">
    {#if searching}
      <p class="status">Searching…</p>
    {:else if query.trim().length < 2}
      <p class="status">Type at least 2 characters.</p>
    {:else if results.length === 0}
      <p class="status">No results for "{query}"</p>
    {:else}
      <div class="grid">
        {#each results as r (r.id)}
          <PosterCard
            title={r.title}
            posterPath={r.poster_path ?? r.thumb_path}
            subtitle={r.year ? String(r.year) : r.type}
            onclick={() => goto(`/item/${r.id}`)}
          />
        {/each}
      </div>
    {/if}
  </section>
</div>

<style>
  .page {
    padding: var(--page-pad);
    display: flex;
    flex-direction: column;
    gap: 32px;
  }

  h1 {
    font-size: var(--font-2xl);
    margin: 0;
  }

  .results {
    min-height: 400px;
  }

  .status {
    font-size: var(--font-md);
    color: var(--text-secondary);
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: var(--card-gap);
  }
</style>
