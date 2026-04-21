<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { libraryApi, mediaApi, type Library, type GenreCount } from '$lib/api';

  let library: Library | null = null;
  let genres: GenreCount[] = [];
  let loading = true;
  let error = '';

  $: id = $page.params.id!;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    try {
      [library, genres] = await Promise.all([libraryApi.get(id), mediaApi.genres(id)]);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed';
    } finally {
      loading = false;
    }
  });

  function pickGenre(g: GenreCount) {
    goto(`/libraries/${id}?genre=${encodeURIComponent(g.name)}`);
  }
</script>

<div class="page">
  <header>
    <a class="back" href="/libraries/{id}">← {library?.name ?? 'Library'}</a>
    <h1>Browse by Genre</h1>
    {#if !loading}<span class="subtle">{genres.length} genres</span>{/if}
  </header>

  {#if loading}
    <p class="loading">Loading…</p>
  {:else if error}
    <p class="err">{error}</p>
  {:else if genres.length === 0}
    <p class="empty">No genres yet. Run a scan with metadata enrichment enabled.</p>
  {:else}
    <div class="grid">
      {#each genres as g}
        <button class="tile" on:click={() => pickGenre(g)}>
          <span class="name">{g.name}</span>
          <span class="count">{g.count.toLocaleString()}</span>
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .page { padding: 1.5rem 2rem; max-width: 1200px; margin: 0 auto; }
  header { display: flex; align-items: baseline; gap: 1rem; margin-bottom: 1.5rem; }
  .back { color: var(--text-secondary); text-decoration: none; font-size: 0.85rem; }
  .back:hover { color: var(--text-primary); }
  h1 { margin: 0; font-size: 1.5rem; }
  .subtle { color: var(--text-secondary); font-size: 0.85rem; }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
    gap: 0.75rem;
  }
  .tile {
    display: flex; justify-content: space-between; align-items: center;
    padding: 0.85rem 1rem;
    background: var(--surface); border: 1px solid var(--border); border-radius: 10px;
    color: var(--text-primary); cursor: pointer; transition: background 0.15s, border-color 0.15s;
    font-size: 0.95rem;
  }
  .tile:hover { background: var(--surface-hover); border-color: var(--accent); }
  .name { font-weight: 500; }
  .count { color: var(--text-secondary); font-size: 0.8rem; }
  .loading, .err, .empty { color: var(--text-secondary); padding: 2rem 0; text-align: center; }
  .err { color: var(--error); }
</style>
