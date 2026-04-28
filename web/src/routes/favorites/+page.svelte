<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { favoritesApi, assetUrl, type FavoriteItem } from '$lib/api';

  let items: FavoriteItem[] = [];
  let loading = true;
  let loadingMore = false;
  let error = '';
  let hasMore = true;
  let ready = false;
  const PAGE_SIZE = 100;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    ready = true;
    await load();
  });

  async function load() {
    loading = true; error = '';
    try {
      const res = await favoritesApi.list(PAGE_SIZE, 0);
      items = res.items;
      hasMore = res.items.length >= PAGE_SIZE;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load favorites';
    } finally { loading = false; }
  }

  async function loadMore() {
    loadingMore = true;
    try {
      const res = await favoritesApi.list(PAGE_SIZE, items.length);
      items = [...items, ...res.items];
      hasMore = res.items.length >= PAGE_SIZE;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load more';
    } finally { loadingMore = false; }
  }
</script>

<svelte:head><title>Favorites - OnScreen</title></svelte:head>

{#if ready}
<div class="page">
  <h1 class="page-title">Favorites</h1>

  {#if error}
    <div class="banner-error">{error}</div>
  {/if}

  {#if loading}
    <div class="grid">
      {#each [1,2,3,4,5,6,7,8,9,10] as _}
        <div class="skeleton-tile"></div>
      {/each}
    </div>
  {:else if items.length === 0}
    <div class="empty">
      <div class="empty-icon">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" width="48" height="48">
          <path d="M12 21.35l-1.45-1.32C5.4 15.36 2 12.28 2 8.5 2 5.42 4.42 3 7.5 3c1.74 0 3.41.81 4.5 2.09C13.09 3.81 14.76 3 16.5 3 19.58 3 22 5.42 22 8.5c0 3.78-3.4 6.86-8.55 11.54L12 21.35z"/>
        </svg>
      </div>
      <p class="empty-title">No favorites yet</p>
      <p class="empty-sub">Tap the heart icon while watching to save items here.</p>
    </div>
  {:else}
    <div class="grid">
      {#each items as it (it.id)}
        <a class="tile" href="/watch/{it.id}">
          <div class="poster">
            {#if it.poster_path}
              <img
                src="{assetUrl('/artwork/' + encodeURI(it.poster_path))}?w=300"
                srcset="{assetUrl('/artwork/' + encodeURI(it.poster_path))}?w=150 150w, {assetUrl('/artwork/' + encodeURI(it.poster_path))}?w=300 300w, {assetUrl('/artwork/' + encodeURI(it.poster_path))}?w=600 600w"
                sizes="(max-width: 768px) 150px, 200px"
                alt={it.title}
                loading="lazy"
              />
            {:else}
              <div class="poster-blank">{it.title[0]?.toUpperCase() ?? '?'}</div>
            {/if}
          </div>
          <div class="tile-title">{it.title}</div>
          {#if it.year}<div class="tile-year">{it.year}</div>{/if}
        </a>
      {/each}
    </div>

    {#if hasMore}
      <div class="load-more-wrap">
        <button class="btn-load-more" disabled={loadingMore} on:click={loadMore}>
          {loadingMore ? 'Loading...' : 'Load more'}
        </button>
      </div>
    {/if}
  {/if}
</div>
{/if}

<style>
  .page { padding: 2.5rem 2.5rem 4rem; max-width: 1400px; }
  .page-title {
    font-size: 1.1rem;
    font-weight: 700;
    color: var(--text-primary);
    letter-spacing: -0.02em;
    margin-bottom: 1.5rem;
  }
  .banner-error {
    background: var(--error-bg);
    border: 1px solid var(--error);
    color: var(--error);
    padding: 0.65rem 1rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.5rem;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 1rem;
  }

  .tile {
    text-decoration: none;
    color: inherit;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .poster {
    aspect-ratio: 2 / 3;
    border-radius: 8px;
    overflow: hidden;
    background: var(--bg-elevated);
    transition: transform 0.15s;
  }
  .tile:hover .poster { transform: translateY(-2px); }
  .poster img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .poster-blank {
    width: 100%;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 2rem;
    color: var(--text-muted);
  }
  .tile-title {
    font-size: 0.82rem;
    font-weight: 500;
    color: var(--text-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .tile-year {
    font-size: 0.72rem;
    color: var(--text-muted);
  }

  .skeleton-tile {
    aspect-ratio: 2 / 3;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, #16161f 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
    border-radius: 8px;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    padding: 6rem 2rem;
    gap: 0.5rem;
  }
  .empty-icon { color: var(--text-muted); margin-bottom: 0.75rem; }
  .empty-title { font-size: 1rem; font-weight: 600; color: var(--text-muted); }
  .empty-sub { font-size: 0.82rem; color: var(--text-muted); }

  .load-more-wrap { display: flex; justify-content: center; margin-top: 1.5rem; }
  .btn-load-more {
    padding: 0.6rem 1.2rem;
    background: var(--bg-elevated);
    color: var(--text-primary);
    border: 1px solid var(--border);
    border-radius: 8px;
    font-size: 0.85rem;
    cursor: pointer;
    transition: background 0.12s;
  }
  .btn-load-more:hover:not(:disabled) { background: var(--bg-hover); }
  .btn-load-more:disabled { opacity: 0.5; cursor: not-allowed; }

  @media (max-width: 600px) {
    .page { padding: 1.25rem 1rem 3rem; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(120px, 1fr)); gap: 0.75rem; }
  }
</style>
