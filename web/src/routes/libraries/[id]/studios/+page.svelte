<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { libraryApi, studiosApi, type Library, type Studio } from '$lib/api';
  import { libraryStudioKind, studioHref, studioKindLabel } from '$lib/studios';

  let library: Library | null = null;
  let studios: Studio[] = [];
  let loading = true;
  let error = '';

  $: id = $page.params.id!;
  // Movie libraries list studios, show libraries networks.
  $: kind = libraryStudioKind(library?.type) ?? 'studio';

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    try {
      library = await libraryApi.get(id);
      studios = await studiosApi.list({ library_id: id, kind: libraryStudioKind(library.type) ?? undefined });
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed';
    } finally {
      loading = false;
    }
  });
</script>

<div class="page">
  <header>
    <a class="back" href="/libraries/{id}">← {library?.name ?? 'Library'}</a>
    <h1>Browse by {studioKindLabel(kind)}</h1>
    {#if !loading}<span class="subtle">{studios.length} {studioKindLabel(kind, true).toLowerCase()}</span>{/if}
  </header>

  {#if loading}
    <p class="loading">Loading…</p>
  {:else if error}
    <p class="err">{error}</p>
  {:else if studios.length === 0}
    <p class="empty">No {studioKindLabel(kind, true).toLowerCase()} yet. They come from TMDB and NFO files when titles are enriched.</p>
  {:else}
    <div class="grid">
      {#each studios as s (s.id)}
        <a class="tile" href={studioHref(s, id)}>
          <span class="name">{s.name}</span>
          <span class="count">{s.item_count.toLocaleString()}</span>
        </a>
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
    grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
    gap: 0.75rem;
  }
  .tile {
    display: flex; justify-content: space-between; align-items: center; gap: 0.5rem;
    padding: 0.85rem 1rem; text-decoration: none;
    background: var(--surface); border: 1px solid var(--border); border-radius: 10px;
    color: var(--text-primary); transition: background 0.15s, border-color 0.15s;
    font-size: 0.95rem;
  }
  .tile:hover { background: var(--surface-hover); border-color: var(--accent); }
  .name { font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .count { color: var(--text-secondary); font-size: 0.8rem; flex-shrink: 0; }
  .loading, .err, .empty { color: var(--text-secondary); padding: 2rem 0; text-align: center; }
  .err { color: var(--error); }
</style>
