<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { libraryApi, mediaApi, type Library, type MediaItem, type SortField, type ListItemsParams } from '$lib/api';
  import PlaylistPicker from '$lib/components/PlaylistPicker.svelte';

  let playlistPickerItemId = '';
  let showPlaylistPicker = false;

  function openPlaylistPicker(e: MouseEvent, itemId: string) {
    e.preventDefault();
    e.stopPropagation();
    playlistPickerItemId = itemId;
    showPlaylistPicker = true;
  }

  let alive = true;
  let enrichingIds = new Set<string>();

  async function enrichItem(e: MouseEvent, itemId: string) {
    e.preventDefault();
    e.stopPropagation();
    const capturedId = id;
    enrichingIds = new Set(enrichingIds).add(itemId);
    try {
      await mediaApi.enrichItem(itemId);
      for (let i = 0; i < 10; i++) {
        if (!alive || id !== capturedId) break;
        await new Promise(r => setTimeout(r, 2000));
        if (!alive || id !== capturedId) break;
        const r = await mediaApi.listItems(capturedId, PAGE, 0, filterParams());
        const updated = r.items.find(x => x.id === itemId);
        if (updated?.poster_path) {
          allItems = allItems.map(x => x.id === itemId ? updated : x);
          break;
        }
      }
    } finally {
      enrichingIds = new Set([...enrichingIds].filter(x => x !== itemId));
    }
  }

  let library: Library | null = null;
  let allItems: MediaItem[] = [];
  let loadingLib = true;
  let loadingItems = false;
  let scanning = false;
  let error = '';
  let enrichTimeout = '';

  const PAGE = 120;
  const BATCH = 36;
  let offset = 0;
  let total = 0;
  let hasMore = false;
  let sentinel: HTMLDivElement;

  let query = '';
  let sortField: SortField = 'title';
  let sortAsc = true;

  // Filters
  let genres: string[] = [];
  let selectedGenre = '';
  let yearMin = '';
  let yearMax = '';
  let ratingMin = '';

  let mounted = false;
  let prevId = '';
  let observer: IntersectionObserver | null = null;

  $: id = $page.params.id!;

  $: isPhotoLibrary = library?.type === 'photo';

  // Client-side text filter on already-loaded items
  $: filtered = query
    ? allItems.filter(i => i.title.toLowerCase().includes(query.toLowerCase()))
    : allItems;

  function filterParams(): ListItemsParams {
    const p: ListItemsParams = { sort: sortField, sort_dir: sortAsc ? 'asc' : 'desc' };
    if (selectedGenre) p.genre = selectedGenre;
    if (yearMin) p.year_min = parseInt(yearMin);
    if (yearMax) p.year_max = parseInt(yearMax);
    if (ratingMin) p.rating_min = parseFloat(ratingMin);
    return p;
  }

  function setupObserver() {
    if (!observer) {
      observer = new IntersectionObserver((entries) => {
        if (entries[0]?.isIntersecting && hasMore && !loadingItems) {
          loadItems(true);
        }
      }, { rootMargin: '400px' });
    }
  }

  // Re-observe whenever the sentinel element binds/re-binds
  $: if (sentinel && observer) {
    observer.disconnect();
    observer.observe(sentinel);
  }

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    setupObserver();
    prevId = id;
    await Promise.all([loadLibrary(), loadItems(), loadGenres()]);
    mounted = true;
  });

  onDestroy(() => { alive = false; observer?.disconnect(); });

  $: if (mounted && id && id !== prevId) {
    prevId = id;
    allItems = [];
    offset = 0;
    total = 0;
    hasMore = true;
    loadingLib = true;
    loadingItems = true;
    error = '';
    library = null;
    genres = [];
    selectedGenre = '';
    loadLibrary();
    loadItems();
    loadGenres();
  }

  async function loadLibrary() {
    try { library = await libraryApi.get(id); }
    catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { loadingLib = false; }
  }

  async function loadGenres() {
    try { genres = await mediaApi.genres(id); }
    catch { /* non-critical */ }
  }

  async function loadItems(append = false) {
    loadingItems = true;
    try {
      const limit = append ? BATCH : PAGE;
      const r = await mediaApi.listItems(id, limit, append ? offset : 0, filterParams());
      allItems = append ? [...allItems, ...r.items] : r.items;
      total = r.total;
      offset = append ? offset + r.items.length : r.items.length;
      hasMore = offset < total;
    } catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { loadingItems = false; }
  }

  async function scan() {
    scanning = true;
    enrichTimeout = '';
    const capturedId = id;
    try {
      await libraryApi.scan(capturedId);
      const prevTotal = total;
      let sawChange = false;
      let enrichDeadline = 0;
      let enrichTimedOut = false;
      for (let i = 0; i < 40; i++) {
        if (!alive || id !== capturedId) break;
        await new Promise(r => setTimeout(r, 3000));
        if (!alive || id !== capturedId) break;
        const r = await mediaApi.listItems(capturedId, PAGE, 0, filterParams());
        allItems = r.items;
        total = r.total;
        offset = r.items.length;
        hasMore = offset < total;
        const countChanged = r.total !== prevTotal || (r.total > 0 && allItems.length === 0);
        if (countChanged && !sawChange) {
          sawChange = true;
          enrichDeadline = Date.now() + 20_000;
        }
        if (sawChange && Date.now() >= enrichDeadline) {
          const missingArt = r.items.some(item => !item.poster_path);
          if (missingArt) enrichTimedOut = true;
          break;
        }
        if (!sawChange && i >= 29) break;
      }
      if (enrichTimedOut) {
        enrichTimeout = 'Enrichment timed out \u2014 artwork may still be loading. Try refreshing later.';
      }
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Scan failed';
    } finally {
      scanning = false;
    }
  }

  async function refresh() { await loadItems(); }

  async function applyFilters() {
    allItems = [];
    offset = 0;
    total = 0;
    hasMore = false;
    await loadItems();
  }

  function toggleSort(f: SortField) {
    if (sortField === f) sortAsc = !sortAsc;
    else { sortField = f; sortAsc = f === 'title'; }
    applyFilters();
  }

  function dur(ms?: number) {
    if (!ms) return '';
    const m = Math.round(ms / 60000);
    return m < 60 ? `${m}m` : `${Math.floor(m / 60)}h ${m % 60}m`;
  }

  const typeColor: Record<string, string> = {
    movie: '#60a5fa', show: '#a78bfa', music: '#34d399', photo: '#fb923c'
  };
  const typeLabel: Record<string, string> = {
    movie: 'Movies', show: 'TV Shows', music: 'Music', photo: 'Photos'
  };
</script>

<svelte:head><title>{library?.name ?? 'Library'} — OnScreen</title></svelte:head>

<div class="page">
  <nav class="crumb">
    <a href="/">Libraries</a>
    <span>/</span>
    <span>{library?.name ?? '…'}</span>
  </nav>

  <!-- Library header -->
  {#if !loadingLib && library}
    {@const color = typeColor[library.type] ?? '#aaa'}
    <div class="lib-head">
      <div>
        <div class="lib-type" style="color:{color}">{typeLabel[library.type] ?? library.type}</div>
        <h1>{library.name}</h1>
        <div class="lib-paths">{(library.scan_paths ?? []).join('  ·  ')}</div>
      </div>
      <div class="head-actions">
        <button class="btn-refresh" title="Reload items" disabled={loadingItems} on:click={refresh}>
          <svg viewBox="0 0 16 16" fill="currentColor" width="13" height="13">
            <path d="M1.705 8.005a.75.75 0 0 1 .834.656 5.5 5.5 0 0 0 9.592 2.97l-1.204-1.204a.25.25 0 0 1 .177-.427h3.646a.25.25 0 0 1 .25.25v3.646a.25.25 0 0 1-.427.177l-1.38-1.38A7.002 7.002 0 0 1 1.05 8.84a.75.75 0 0 1 .656-.834ZM8 2.5a5.487 5.487 0 0 0-4.131 1.869l1.204 1.204A.25.25 0 0 1 4.896 6H1.25A.25.25 0 0 1 1 5.75V2.104a.25.25 0 0 1 .427-.177l1.38 1.38A7.002 7.002 0 0 1 14.95 7.16a.75.75 0 0 1-1.49.178A5.5 5.5 0 0 0 8 2.5Z"/>
          </svg>
        </button>
        <button class="btn-scan" class:running={scanning} disabled={scanning} on:click={scan}>
          {#if scanning}
            <span class="spin">⟳</span> Scanning…
          {:else}
            <svg viewBox="0 0 16 16" fill="currentColor" width="13" height="13">
              <path fill-rule="evenodd" d="M8 2.5A5.5 5.5 0 1013.5 8a.75.75 0 011.5 0 7 7 0 11-3.5-6.062V.75a.75.75 0 011.5 0v3a.75.75 0 01-.75.75h-3a.75.75 0 010-1.5h1.335A5.472 5.472 0 008 2.5z" clip-rule="evenodd"/>
            </svg>
            Scan
          {/if}
        </button>
        <a href="/libraries/{id}/settings" class="btn-settings">Settings</a>
      </div>
    </div>
  {/if}

  {#if error}
    <div class="error-bar">{error}</div>
  {/if}
  {#if enrichTimeout}
    <div class="error-bar">{enrichTimeout}</div>
  {/if}

  <!-- Controls -->
  <div class="controls">
    <div class="search-box">
      <svg viewBox="0 0 16 16" fill="currentColor" width="13" height="13" class="search-ico">
        <path d="M6.02 2a4.02 4.02 0 100 8.04A4.02 4.02 0 006.02 2zm-5.52 4.02a5.52 5.52 0 119.842 3.461l3.11 3.11a.75.75 0 11-1.061 1.06l-3.11-3.11A5.52 5.52 0 01.5 6.02z"/>
      </svg>
      <input bind:value={query} placeholder="Filter…" />
      {#if query}<button class="clear-btn" on:click={() => query = ''}>×</button>{/if}
    </div>

    <div class="sort-row">
      {#each [['title','Title'],['year','Year'],['rating','Rating'],['created_at','Added']] as [f, l]}
        <button class="sort-pill" class:on={sortField === f} on:click={() => toggleSort(f as SortField)}>
          {l}{sortField === f ? (sortAsc ? ' ↑' : ' ↓') : ''}
        </button>
      {/each}
    </div>

    {#if genres.length > 0}
      <select class="filter-select" bind:value={selectedGenre} on:change={applyFilters}>
        <option value="">All Genres</option>
        {#each genres as g}
          <option value={g}>{g}</option>
        {/each}
      </select>
    {/if}

    <div class="count">
      {#if query}{filtered.length} / {allItems.length}{:else}{total} items{/if}
    </div>
  </div>

  <!-- Grid -->
  {#if allItems.length === 0 && !loadingItems}
    <div class="empty">
      <div class="empty-icon">⬡</div>
      <p class="empty-t">Library is empty</p>
      <p class="empty-s">Run a scan to find media files.</p>
      <button class="btn-scan" on:click={scan}>Scan Now</button>
    </div>
  {:else if filtered.length === 0}
    <div class="empty">
      <p class="empty-t">No results for "{query}"</p>
      <button class="clear-link" on:click={() => query = ''}>Clear filter</button>
    </div>
  {:else}
    <div class="grid" class:photo-grid={isPhotoLibrary}>
      {#each filtered as item (item.id)}
        <a class="item" href="/watch/{item.id}" tabindex="0">
          <div class="poster">
            {#if item.poster_path}
              <img src="/artwork/{item.poster_path}?v={item.updated_at}&w=300"
                   srcset="/artwork/{item.poster_path}?v={item.updated_at}&w=150 150w, /artwork/{item.poster_path}?v={item.updated_at}&w=300 300w, /artwork/{item.poster_path}?v={item.updated_at}&w=450 450w"
                   sizes="(max-width: 768px) 100px, 180px"
                   alt={item.title} loading="lazy" />
            {:else}
              <div class="poster-blank">
                <span>{item.title[0]?.toUpperCase()}</span>
              </div>
            {/if}
            <div class="poster-overlay">
              {#if !isPhotoLibrary}<div class="play-icon">▶</div>{/if}
              <div class="overlay-title">{item.title}</div>
              {#if !isPhotoLibrary}
                <div class="overlay-meta">
                  {#if item.year}{item.year}{/if}
                  {#if item.duration_ms} · {dur(item.duration_ms)}{/if}
                </div>
              {/if}
            </div>
            {#if item.rating}
              <div class="rating">{item.rating.toFixed(1)}</div>
            {/if}
            {#if !item.poster_path}
              <button
                class="refresh-art"
                class:spinning={enrichingIds.has(item.id)}
                title="Refresh artwork"
                on:click={(e) => enrichItem(e, item.id)}
              >⟳</button>
            {/if}
            <button
              class="add-playlist-btn"
              title="Add to playlist"
              on:click={(e) => openPlaylistPicker(e, item.id)}
            >+</button>
          </div>
          <div class="item-foot">
            <div class="item-title">{item.title}</div>
            {#if item.year}<div class="item-year">{item.year}</div>{/if}
          </div>
        </a>
      {/each}

      {#if loadingItems}
        {#each {length: 8} as _}
          <div class="item skeleton-item">
            <div class="poster skeleton-poster"></div>
          </div>
        {/each}
      {/if}
    </div>

    {#if hasMore}
      <div class="scroll-sentinel" bind:this={sentinel}></div>
      {#if loadingItems}
        <div class="loading-more">
          <span class="spin">&#8635;</span> Loading…
        </div>
      {/if}
    {/if}
  {/if}
</div>

<PlaylistPicker
  mediaItemId={playlistPickerItemId}
  open={showPlaylistPicker}
  on:close={() => showPlaylistPicker = false}
/>

<style>
  .page { padding: 2.5rem 2.5rem 5rem; }

  .crumb {
    display: flex; align-items: center; gap: 0.4rem;
    font-size: 0.75rem; color: var(--text-muted); margin-bottom: 1.5rem;
  }
  .crumb a { color: var(--text-muted); text-decoration: none; }
  .crumb a:hover { color: var(--text-secondary); }

  .lib-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 2rem;
    margin-bottom: 2rem;
    padding-bottom: 2rem;
    border-bottom: 1px solid var(--border);
  }
  .lib-type {
    font-size: 0.68rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    opacity: 0.8;
    margin-bottom: 0.3rem;
  }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); letter-spacing: -0.025em; margin-bottom: 0.3rem; }
  .lib-paths { font-size: 0.75rem; color: var(--text-muted); font-family: monospace; }

  .head-actions { display: flex; gap: 0.5rem; align-items: center; flex-shrink: 0; }

  .btn-scan {
    display: inline-flex; align-items: center; gap: 0.4rem;
    padding: 0.42rem 0.85rem;
    background: var(--accent-bg);
    border: 1px solid rgba(124,106,247,0.25);
    border-radius: 7px;
    color: var(--accent-text);
    font-size: 0.78rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.12s;
  }
  .btn-scan:hover { background: rgba(124,106,247,0.2); }
  .btn-scan.running { opacity: 0.6; cursor: not-allowed; }
  .btn-scan:disabled { cursor: not-allowed; }
  .spin { display: inline-block; animation: spin 0.8s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }

  .btn-settings {
    padding: 0.42rem 0.85rem;
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-muted);
    font-size: 0.78rem;
    text-decoration: none;
    transition: border-color 0.12s, color 0.12s;
  }
  .btn-settings:hover { border-color: var(--accent-bg); color: var(--text-secondary); }

  .btn-refresh {
    display: inline-flex; align-items: center; justify-content: center;
    width: 30px; height: 30px;
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    color: var(--text-muted);
    cursor: pointer;
    transition: border-color 0.12s, color 0.12s;
  }
  .btn-refresh:hover { border-color: var(--accent-bg); color: var(--text-secondary); }
  .btn-refresh:disabled { opacity: 0.4; cursor: not-allowed; }


  .error-bar {
    background: var(--error-bg);
    border: 1px solid var(--error-bg);
    color: var(--error);
    padding: 0.6rem 0.9rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1.5rem;
  }

  .controls {
    display: flex;
    align-items: center;
    gap: 1rem;
    margin-bottom: 1.75rem;
    flex-wrap: wrap;
  }

  .search-box {
    position: relative;
    flex: 0 0 220px;
    display: flex;
    align-items: center;
  }
  .search-ico {
    position: absolute;
    left: 0.65rem;
    color: var(--text-muted);
    pointer-events: none;
  }
  .search-box input {
    width: 100%;
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.42rem 1.75rem 0.42rem 2rem;
    font-size: 0.8rem;
    color: var(--text-primary);
    transition: border-color 0.15s;
  }
  .search-box input:focus { outline: none; border-color: var(--accent); }
  ::placeholder { color: var(--text-muted); }
  .clear-btn {
    position: absolute; right: 0.5rem;
    background: none; border: none; color: var(--text-muted);
    font-size: 1rem; cursor: pointer; padding: 0 0.2rem; line-height: 1;
  }
  .clear-btn:hover { color: var(--text-secondary); }

  .sort-row { display: flex; gap: 4px; }
  .sort-pill {
    padding: 0.35rem 0.65rem;
    background: var(--input-bg);
    border: 1px solid var(--border);
    border-radius: 20px;
    font-size: 0.72rem;
    color: var(--text-muted);
    cursor: pointer;
    transition: all 0.12s;
    white-space: nowrap;
  }
  .sort-pill:hover { background: var(--border); color: var(--text-secondary); }
  .sort-pill.on { background: rgba(124,106,247,0.1); border-color: rgba(124,106,247,0.3); color: var(--accent-text); }

  .filter-select {
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    padding: 0.35rem 0.6rem;
    font-size: 0.75rem;
    color: var(--text-secondary);
    cursor: pointer;
  }
  .filter-select:focus { outline: none; border-color: var(--accent); }
  .filter-select option { background: var(--bg-elevated); color: var(--text-primary); }

  .count { margin-left: auto; font-size: 0.75rem; color: var(--text-muted); white-space: nowrap; }

  /* Poster grid */
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(130px, 1fr));
    gap: 1rem;
  }
  .photo-grid {
    grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
    gap: 0.5rem;
  }

  .item { display: flex; flex-direction: column; text-decoration: none; color: inherit; }

  .photo-grid .poster {
    aspect-ratio: 4/3;
  }
  .photo-grid .poster img {
    object-fit: cover;
  }

  .poster {
    aspect-ratio: 2/3;
    border-radius: 8px;
    overflow: hidden;
    position: relative;
    background: var(--bg-elevated);
    cursor: pointer;
  }
  .poster img { width: 100%; height: 100%; object-fit: cover; display: block; transition: transform 0.3s; }
  .item:hover .poster img { transform: scale(1.04); }

  .poster-blank {
    width: 100%;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    background: linear-gradient(135deg, var(--bg-secondary), var(--bg-primary));
  }
  .poster-blank span {
    font-size: 2.5rem;
    font-weight: 800;
    color: var(--text-muted);
    line-height: 1;
  }

  .poster-overlay {
    position: absolute;
    inset: 0;
    background: linear-gradient(to top, rgba(0,0,0,0.85) 0%, transparent 50%);
    display: flex;
    flex-direction: column;
    justify-content: flex-end;
    padding: 0.6rem;
    opacity: 0;
    transition: opacity 0.2s;
  }
  .item:hover .poster-overlay { opacity: 1; }
  .play-icon {
    font-size: 1.5rem;
    color: rgba(255,255,255,0.9);
    margin-bottom: 0.3rem;
    text-shadow: 0 2px 8px rgba(0,0,0,0.6);
  }
  .overlay-title { font-size: 0.72rem; font-weight: 700; color: #fff; line-height: 1.3; }
  .overlay-meta { font-size: 0.65rem; color: rgba(255,255,255,0.55); margin-top: 0.15rem; }

  .rating {
    position: absolute;
    top: 0.4rem;
    right: 0.4rem;
    background: rgba(0,0,0,0.7);
    color: #fbbf24;
    font-size: 0.62rem;
    font-weight: 700;
    padding: 0.15rem 0.3rem;
    border-radius: 4px;
    backdrop-filter: blur(4px);
  }

  .refresh-art {
    position: absolute;
    bottom: 0.35rem;
    right: 0.35rem;
    background: rgba(0,0,0,0.65);
    border: none;
    border-radius: 50%;
    color: rgba(255,255,255,0.6);
    font-size: 0.85rem;
    width: 24px;
    height: 24px;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    opacity: 0;
    transition: opacity 0.15s, color 0.15s;
    line-height: 1;
    padding: 0;
  }
  .item:hover .refresh-art { opacity: 1; }
  .refresh-art:hover { color: #fff; }
  .refresh-art.spinning { animation: spin 0.8s linear infinite; opacity: 1; }
  @keyframes spin { to { transform: rotate(360deg); } }

  .add-playlist-btn {
    position: absolute;
    bottom: 0.35rem;
    left: 0.35rem;
    background: rgba(0,0,0,0.65);
    border: none;
    border-radius: 50%;
    color: rgba(255,255,255,0.6);
    font-size: 1rem;
    width: 24px;
    height: 24px;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    opacity: 0;
    transition: opacity 0.15s, color 0.15s;
    line-height: 1;
  }
  .item:hover .add-playlist-btn { opacity: 1; }
  .add-playlist-btn:hover { color: var(--accent); }

  .item-foot { padding: 0.4rem 0.1rem 0; }
  .item-title {
    font-size: 0.75rem;
    font-weight: 500;
    color: var(--text-secondary);
    line-height: 1.3;
    display: -webkit-box;
    -webkit-line-clamp: 1;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .item-year { font-size: 0.68rem; color: var(--text-muted); }

  /* Skeleton */
  .skeleton-item { pointer-events: none; }
  .skeleton-poster {
    aspect-ratio: 2/3;
    border-radius: 8px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, #16161f 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .empty {
    display: flex; flex-direction: column; align-items: center;
    padding: 6rem 2rem; text-align: center; gap: 0.4rem;
  }
  .empty-icon { font-size: 2rem; color: var(--text-muted); margin-bottom: 0.75rem; }
  .empty-t { font-size: 0.9rem; font-weight: 600; color: var(--text-muted); }
  .empty-s { font-size: 0.78rem; color: var(--text-muted); margin-bottom: 1rem; }
  .clear-link {
    background: none; border: none; color: var(--accent); font-size: 0.8rem; cursor: pointer;
    text-decoration: underline;
  }

  .scroll-sentinel { height: 1px; }
  .loading-more {
    text-align: center;
    padding: 1.5rem 0;
    font-size: 0.8rem;
    color: var(--text-muted);
  }

  /* ── Mobile ────────────────────────────────────────────────────────────── */
  @media (max-width: 768px) {
    .page { padding: 1.25rem 1rem 5rem; }

    .lib-head {
      flex-direction: column;
      gap: 1rem;
    }
    .head-actions { flex-wrap: wrap; }

    .controls { gap: 0.65rem; }
    .search-box { flex: 1 1 100%; }
    .sort-row { flex-wrap: wrap; gap: 4px; }

    .grid {
      grid-template-columns: repeat(auto-fill, minmax(100px, 1fr));
      gap: 0.65rem;
    }

    .poster-blank span { font-size: 1.8rem; }
    .item-title { font-size: 0.68rem; }
    .item-year { font-size: 0.6rem; }
  }
</style>
