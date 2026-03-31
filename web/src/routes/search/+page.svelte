<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { searchApi, type SearchResult } from '$lib/api';

  let query = '';
  let results: SearchResult[] = [];
  let loading = false;
  let searched = false;
  let debounceTimer: ReturnType<typeof setTimeout>;

  onMount(() => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
  });

  function onInput() {
    clearTimeout(debounceTimer);
    const q = query.trim();
    if (!q) {
      results = [];
      searched = false;
      loading = false;
      return;
    }
    loading = true;
    debounceTimer = setTimeout(() => doSearch(q), 300);
  }

  async function doSearch(q: string) {
    try {
      results = await searchApi.search(q) ?? [];
    } catch (e) {
      console.warn('search failed', e);
      results = [];
    } finally {
      loading = false;
      searched = true;
    }
  }

  function navigate(item: SearchResult) {
    if (item.type === 'movie' || item.type === 'episode') {
      goto(`/watch/${item.id}`);
    } else {
      // shows/seasons navigate to item detail
      goto(`/watch/${item.id}`);
    }
  }

  const typeBadge: Record<string, { label: string; color: string }> = {
    movie:   { label: 'Movie',   color: '#60a5fa' },
    show:    { label: 'Show',    color: '#a78bfa' },
    season:  { label: 'Season',  color: '#818cf8' },
    episode: { label: 'Episode', color: '#67e8f9' },
    music:   { label: 'Music',   color: '#34d399' },
  };
</script>

<svelte:head><title>Search - OnScreen</title></svelte:head>

<div class="page">
  <h1>Search</h1>

  <div class="search-box">
    <svg class="search-icon" viewBox="0 0 20 20" fill="currentColor" width="16" height="16">
      <path fill-rule="evenodd" d="M9 3.5a5.5 5.5 0 100 11 5.5 5.5 0 000-11zM2 9a7 7 0 1112.452 4.391l3.328 3.329a.75.75 0 11-1.06 1.06l-3.329-3.328A7 7 0 012 9z" clip-rule="evenodd"/>
    </svg>
    <input
      type="text"
      bind:value={query}
      on:input={onInput}
      placeholder="Search movies, shows, episodes..."
      autofocus
    />
    {#if query}
      <button class="clear-btn" on:click={() => { query = ''; results = []; searched = false; }}>
        <svg viewBox="0 0 16 16" fill="currentColor" width="14" height="14">
          <path d="M3.72 3.72a.75.75 0 011.06 0L8 6.94l3.22-3.22a.75.75 0 111.06 1.06L9.06 8l3.22 3.22a.75.75 0 11-1.06 1.06L8 9.06l-3.22 3.22a.75.75 0 01-1.06-1.06L6.94 8 3.72 4.78a.75.75 0 010-1.06z"/>
        </svg>
      </button>
    {/if}
  </div>

  {#if loading}
    <div class="results-grid">
      {#each [1,2,3,4,5,6] as _}
        <div class="result-card skeleton"></div>
      {/each}
    </div>
  {:else if searched && results.length === 0}
    <div class="empty">
      <div class="empty-glyph">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" width="48" height="48">
          <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z"/>
        </svg>
      </div>
      <p class="empty-title">No results for "{query}"</p>
      <p class="empty-sub">Try a different search term or check your spelling.</p>
    </div>
  {:else if results.length > 0}
    <div class="results-grid">
      {#each results as item (item.id)}
        {@const badge = typeBadge[item.type] ?? { label: item.type, color: '#888' }}
        <button class="result-card" on:click={() => navigate(item)}>
          <div class="poster-wrap">
            {#if item.poster_path || item.thumb_path}
              <img src="/artwork/{item.poster_path ?? item.thumb_path}?w=300"
                   srcset="/artwork/{item.poster_path ?? item.thumb_path}?w=150 150w, /artwork/{item.poster_path ?? item.thumb_path}?w=300 300w, /artwork/{item.poster_path ?? item.thumb_path}?w=450 450w"
                   sizes="(max-width: 768px) 100px, 180px"
                   alt={item.title} loading="lazy" />
            {:else}
              <div class="poster-blank">
                <span>{item.title[0]?.toUpperCase() ?? '?'}</span>
              </div>
            {/if}
          </div>
          <div class="result-info">
            <span class="type-badge" style="color:{badge.color};border-color:{badge.color}">{badge.label}</span>
            <div class="result-title">{item.title}</div>
            {#if item.year}
              <div class="result-year">{item.year}</div>
            {/if}
          </div>
        </button>
      {/each}
    </div>
  {:else}
    <div class="empty">
      <div class="empty-glyph">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" width="48" height="48">
          <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z"/>
        </svg>
      </div>
      <p class="empty-title">Search your library</p>
      <p class="empty-sub">Type above to find movies, shows, and episodes.</p>
    </div>
  {/if}
</div>

<style>
  .page { padding: 2.5rem 2.5rem 4rem; max-width: 1200px; }

  h1 {
    font-size: 1.1rem;
    font-weight: 700;
    color: var(--text-primary);
    letter-spacing: -0.02em;
    margin-bottom: 1.25rem;
  }

  .search-box {
    position: relative;
    margin-bottom: 2rem;
  }
  .search-icon {
    position: absolute;
    left: 0.85rem;
    top: 50%;
    transform: translateY(-50%);
    color: var(--text-muted);
    pointer-events: none;
  }
  .search-box input {
    width: 100%;
    padding: 0.65rem 2.5rem 0.65rem 2.5rem;
    background: var(--bg-elevated);
    border: 1px solid var(--border-strong);
    border-radius: 10px;
    color: var(--text-primary);
    font-size: 0.88rem;
    outline: none;
    transition: border-color 0.15s;
  }
  .search-box input::placeholder { color: var(--text-muted); }
  .search-box input:focus { border-color: rgba(124,106,247,0.5); }
  .clear-btn {
    position: absolute;
    right: 0.6rem;
    top: 50%;
    transform: translateY(-50%);
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    padding: 0.2rem;
    display: flex;
    transition: color 0.12s;
  }
  .clear-btn:hover { color: var(--text-secondary); }

  /* ── Results grid ──────────────────────────────────────────────────────── */
  .results-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 1rem;
  }

  .result-card {
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 8px;
    overflow: hidden;
    cursor: pointer;
    transition: transform 0.15s, box-shadow 0.15s;
    text-align: left;
    color: inherit;
    padding: 0;
    font-family: inherit;
  }
  .result-card:hover { transform: translateY(-3px); box-shadow: 0 8px 24px var(--shadow); }

  .poster-wrap img {
    width: 100%;
    aspect-ratio: 2/3;
    object-fit: cover;
    display: block;
  }
  .poster-blank {
    width: 100%;
    aspect-ratio: 2/3;
    display: flex;
    align-items: center;
    justify-content: center;
    background: linear-gradient(135deg, var(--bg-secondary), var(--bg-primary));
    font-size: 2rem;
    font-weight: 700;
    color: var(--border-strong);
  }

  .result-info { padding: 0.45rem 0.55rem 0.55rem; }

  .type-badge {
    display: inline-block;
    font-size: 0.58rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    border: 1px solid;
    border-radius: 3px;
    padding: 0.1rem 0.3rem;
    margin-bottom: 0.25rem;
    opacity: 0.85;
  }

  .result-title {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--text-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    line-height: 1.3;
  }
  .result-year {
    font-size: 0.65rem;
    color: var(--text-muted);
    margin-top: 0.1rem;
  }

  .result-card.skeleton {
    aspect-ratio: 2/3;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, #16161f 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
    border: none;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  /* ── Empty state ──────────────────────────────────────────────────────── */
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    padding: 5rem 2rem;
    gap: 0.5rem;
  }
  .empty-glyph { color: var(--text-muted); margin-bottom: 0.75rem; }
  .empty-title { font-size: 1rem; font-weight: 600; color: var(--text-muted); }
  .empty-sub { font-size: 0.82rem; color: var(--text-muted); }

  /* ── Mobile ────────────────────────────────────────────────────────────── */
  @media (max-width: 768px) {
    .page { padding: 1.25rem 1rem 5rem; }

    .results-grid {
      grid-template-columns: repeat(2, 1fr);
      gap: 0.75rem;
    }

    .empty { padding: 3rem 1rem; }
  }
</style>
