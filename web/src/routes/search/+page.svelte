<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import {
    searchApi,
    discoverApi,
    requestsApi,
    assetUrl,
    type SearchResult,
    type DiscoverItem,
  } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let query = '';

  // Library results — items already in the user's collection.
  let libraryResults: SearchResult[] = [];
  let libraryLoading = false;

  // Type filters for the library section. Movies + shows default on
  // because those are the common-case searches; episodes and tracks
  // default off to reduce noise (a search for "Dream" otherwise buries
  // the three movie matches under fifty Pink Floyd track names).
  // Persisted in localStorage so the user's preference survives
  // reloads without a settings UI.
  type LibraryFilters = { movie: boolean; show: boolean; episode: boolean; track: boolean };
  const defaultFilters: LibraryFilters = { movie: true, show: true, episode: false, track: false };
  let libraryFilters: LibraryFilters = { ...defaultFilters };

  // TMDB discover results — anything available to request, minus the
  // entries that already exist in the library (those collapse into the
  // library section so the same title doesn't appear twice).
  let discoverResults: DiscoverItem[] = [];
  let discoverLoading = false;
  let discoverError = ''; // surfaces TMDB API failures (no key configured, rate-limited, etc.)

  let searched = false;
  let debounceTimer: ReturnType<typeof setTimeout>;
  let creatingFor = new Set<number>(); // tmdb_ids of in-flight Request clicks

  onMount(() => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    try {
      const saved = localStorage.getItem('onscreen_search_filters');
      if (saved) {
        const parsed = JSON.parse(saved) as Partial<LibraryFilters>;
        libraryFilters = { ...defaultFilters, ...parsed };
      }
    } catch { /* corrupt storage — fall back to defaults */ }
  });

  $: {
    // Persist whenever the filter set changes. Wrapped in try because
    // Safari private mode throws on setItem.
    try { localStorage.setItem('onscreen_search_filters', JSON.stringify(libraryFilters)); } catch { /* quota / private mode */ }
  }

  // Filtered view of libraryResults honoring the checkbox state. The
  // album + artist + season types stay out of the filter UI (four
  // boxes is already plenty) but still render when their type is on
  // — an album match piggybacks on "Track" since an album IS music,
  // and a season match piggybacks on "Episode".
  $: visibleLibraryResults = libraryResults.filter(r => {
    switch (r.type) {
      case 'movie':   return libraryFilters.movie;
      case 'show':    return libraryFilters.show;
      case 'season':  return libraryFilters.show;
      case 'episode': return libraryFilters.episode;
      case 'artist':  return libraryFilters.track;
      case 'album':   return libraryFilters.track;
      case 'track':   return libraryFilters.track;
      default:        return true; // unknown types (photo, future) always show
    }
  });

  // Titles already in the user's library, normalized for matching so
  // "Star Wars" and "star wars " collapse. Used to hide discover
  // items whose in_library flag missed them (TMDB id mismatch,
  // library item scanned pre-enricher, etc.) — server-side flag is
  // still primary, this is the belt-and-suspenders filter.
  $: libraryTitleKeys = (() => {
    const s = new Set<string>();
    for (const r of libraryResults) {
      if (r.type !== 'movie' && r.type !== 'show') continue;
      const base = normalizeTitle(r.title);
      s.add(base); // bare title — catches re-release year mismatches
      if (r.year) s.add(base + '|' + r.year);
    }
    return s;
  })();

  $: visibleDiscoverResults = discoverResults.filter(it => {
    if (it.in_library) return false;
    if (libraryTitleKeys.has(normalizeTitle(it.title))) return false;
    return true;
  });

  function normalizeTitle(s: string): string {
    return s.toLowerCase().replace(/[^a-z0-9]+/g, ' ').trim();
  }

  function onInput() {
    clearTimeout(debounceTimer);
    const q = query.trim();
    if (!q) {
      libraryResults = [];
      discoverResults = [];
      searched = false;
      libraryLoading = false;
      discoverLoading = false;
      discoverError = '';
      return;
    }
    libraryLoading = true;
    discoverLoading = true;
    debounceTimer = setTimeout(() => doSearch(q), 300);
  }

  // Run library + TMDB searches in parallel. Library always succeeds (it's
  // local DB). Discover may fail when TMDB isn't configured — that's a soft
  // error; we still show library results and surface a hint instead of a
  // page-level failure.
  async function doSearch(q: string) {
    const libraryPromise = searchApi.search(q)
      .then(r => libraryResults = r ?? [])
      .catch(e => { console.warn('library search failed', e); libraryResults = []; })
      .finally(() => libraryLoading = false);

    const discoverPromise = discoverApi.search(q, 12)
      .then(r => {
        // Drop in-library entries — they're already in libraryResults
        // above, so the side-by-side rendering would dupe them.
        discoverResults = (r ?? []).filter(it => !it.in_library);
        discoverError = '';
      })
      .catch(e => {
        const msg = e instanceof Error ? e.message : 'Discover failed';
        // 404 / 503 = TMDB key not configured; that's the operator's
        // setup, not a user-facing error worth shouting about.
        discoverError = /not configured|tmdb/i.test(msg) ? '' : msg;
        discoverResults = [];
      })
      .finally(() => discoverLoading = false);

    await Promise.allSettled([libraryPromise, discoverPromise]);
    searched = true;
  }

  function navigate(item: SearchResult) {
    goto(`/watch/${item.id}`);
  }

  async function requestItem(item: DiscoverItem) {
    creatingFor = new Set(creatingFor).add(item.tmdb_id);
    try {
      const created = await requestsApi.create({ type: item.type, tmdb_id: item.tmdb_id });
      toast.success(`Requested: ${item.title}`);
      // Mirror the server's response into the row so the card flips
      // immediately without a re-search.
      discoverResults = discoverResults.map(r =>
        r.tmdb_id === item.tmdb_id && r.type === item.type
          ? { ...r, has_active_request: true, active_request_id: created.id, active_request_status: created.status }
          : r,
      );
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Request failed');
    } finally {
      const next = new Set(creatingFor);
      next.delete(item.tmdb_id);
      creatingFor = next;
    }
  }

  function statusLabel(s: string): string {
    return s.charAt(0).toUpperCase() + s.slice(1);
  }

  const typeBadge: Record<string, { label: string; color: string }> = {
    movie:   { label: 'Movie',   color: '#60a5fa' },
    show:    { label: 'Show',    color: '#a78bfa' },
    season:  { label: 'Season',  color: '#818cf8' },
    episode: { label: 'Episode', color: '#67e8f9' },
    music:   { label: 'Music',   color: '#34d399' },
  };

  $: anyLoading = libraryLoading || discoverLoading;
  $: nothingFound = searched && !anyLoading && libraryResults.length === 0 && discoverResults.length === 0;
</script>

<svelte:head><title>Search — OnScreen</title></svelte:head>

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
      placeholder="Search your library, or anything to request…"
      autofocus
    />
    {#if query}
      <button class="clear-btn" on:click={() => { query = ''; libraryResults = []; discoverResults = []; searched = false; discoverError = ''; }}>
        <svg viewBox="0 0 16 16" fill="currentColor" width="14" height="14">
          <path d="M3.72 3.72a.75.75 0 011.06 0L8 6.94l3.22-3.22a.75.75 0 111.06 1.06L9.06 8l3.22 3.22a.75.75 0 11-1.06 1.06L8 9.06l-3.22 3.22a.75.75 0 01-1.06-1.06L6.94 8 3.72 4.78a.75.75 0 010-1.06z"/>
        </svg>
      </button>
    {/if}
  </div>

  {#if !query}
    <div class="empty">
      <div class="empty-glyph">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" width="48" height="48">
          <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z"/>
        </svg>
      </div>
      <p class="empty-title">Search your library</p>
      <p class="empty-sub">Type above to find something — if it's not in your library, you can request it.</p>
    </div>
  {:else}
    <!-- ── In your library ─────────────────────────────────────────────── -->
    <section class="result-section">
      <div class="section-head">
        <h2 class="section-title">In your library</h2>
        <div class="filter-row">
          <label class="filter-chip">
            <input type="checkbox" bind:checked={libraryFilters.movie} />
            <span>Movies</span>
          </label>
          <label class="filter-chip">
            <input type="checkbox" bind:checked={libraryFilters.show} />
            <span>Shows</span>
          </label>
          <label class="filter-chip">
            <input type="checkbox" bind:checked={libraryFilters.episode} />
            <span>Episodes</span>
          </label>
          <label class="filter-chip">
            <input type="checkbox" bind:checked={libraryFilters.track} />
            <span>Tracks</span>
          </label>
        </div>
      </div>
      {#if libraryLoading}
        <div class="results-grid">
          {#each [1,2,3,4,5,6] as _}
            <div class="result-card skeleton"></div>
          {/each}
        </div>
      {:else if visibleLibraryResults.length === 0}
        <p class="section-empty">
          {#if libraryResults.length === 0}
            No matches in your library.
          {:else}
            {libraryResults.length} match{libraryResults.length === 1 ? '' : 'es'} hidden by the type filters above.
          {/if}
        </p>
      {:else}
        <div class="results-grid">
          {#each visibleLibraryResults as item (item.id)}
            {@const badge = typeBadge[item.type] ?? { label: item.type, color: '#888' }}
            {@const art = item.poster_path ?? item.thumb_path ?? ''}
            <button class="result-card" on:click={() => navigate(item)}>
              <div class="poster-wrap">
                {#if art}
                  <img src={assetUrl(`/artwork/${encodeURI(art)}?w=300`)}
                       srcset="{assetUrl(`/artwork/${encodeURI(art)}?w=150`)} 150w, {assetUrl(`/artwork/${encodeURI(art)}?w=300`)} 300w, {assetUrl(`/artwork/${encodeURI(art)}?w=450`)} 450w"
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
                {#if item.year}<div class="result-year">{item.year}</div>{/if}
              </div>
            </button>
          {/each}
        </div>
      {/if}
    </section>

    <!-- ── Request from outside library ────────────────────────────────── -->
    <section class="result-section">
      <h2 class="section-title">Request</h2>
      {#if discoverError}
        <div class="banner-error">{discoverError}</div>
      {:else if discoverLoading}
        <div class="results-grid">
          {#each [1,2,3,4,5,6] as _}
            <div class="result-card skeleton"></div>
          {/each}
        </div>
      {:else if visibleDiscoverResults.length === 0}
        <p class="section-empty">No additional matches available to request.</p>
      {:else}
        <div class="results-grid">
          {#each visibleDiscoverResults as item (item.type + ':' + item.tmdb_id)}
            {@const badge = typeBadge[item.type] ?? { label: item.type, color: '#888' }}
            <div class="result-card discover">
              <div class="poster-wrap">
                {#if item.poster_url}
                  <img src={item.poster_url} alt={item.title} loading="lazy" />
                {:else}
                  <div class="poster-blank">
                    <span>{item.title[0]?.toUpperCase() ?? '?'}</span>
                  </div>
                {/if}
                <span class="type-pill type-{item.type}">{badge.label}</span>
              </div>
              <div class="result-info">
                <div class="result-title" title={item.title}>{item.title}</div>
                {#if item.year}<div class="result-year">{item.year}</div>{/if}
                {#if item.has_active_request}
                  <span class="status-pill status-{item.active_request_status ?? 'pending'}">
                    {statusLabel(item.active_request_status ?? 'pending')}
                  </span>
                {:else}
                  <button
                    class="request-btn"
                    disabled={creatingFor.has(item.tmdb_id)}
                    on:click={() => requestItem(item)}
                  >
                    {creatingFor.has(item.tmdb_id) ? 'Requesting…' : 'Request'}
                  </button>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </section>

    {#if nothingFound}
      <div class="empty">
        <div class="empty-glyph">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" width="48" height="48">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z"/>
          </svg>
        </div>
        <p class="empty-title">No results for "{query}"</p>
        <p class="empty-sub">Try a different spelling or check the title.</p>
      </div>
    {/if}
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

  /* ── Sections ─────────────────────────────────────────────────────────── */
  .result-section { margin-bottom: 2.5rem; }
  .section-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 1rem;
    flex-wrap: wrap;
    margin-bottom: 0.85rem;
  }
  .section-title {
    font-size: 0.92rem;
    font-weight: 700;
    color: var(--text-primary);
    letter-spacing: -0.01em;
    margin: 0;
  }
  .filter-row {
    display: flex;
    gap: 0.4rem;
    flex-wrap: wrap;
  }
  .filter-chip {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.25rem 0.6rem;
    background: var(--bg-elevated);
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    font-size: 0.78rem;
    color: var(--text-muted);
    cursor: pointer;
    user-select: none;
    transition: color 0.15s, border-color 0.15s, background 0.15s;
  }
  .filter-chip:hover { color: var(--text-primary); }
  .filter-chip:has(input:checked) {
    color: var(--text-primary);
    border-color: var(--accent, #60a5fa);
    background: color-mix(in srgb, var(--accent, #60a5fa) 12%, var(--bg-elevated));
  }
  .filter-chip input {
    width: 12px; height: 12px;
    margin: 0;
    accent-color: var(--accent, #60a5fa);
    cursor: pointer;
  }
  .section-empty {
    font-size: 0.78rem;
    color: var(--text-muted);
    padding: 0.5rem 0 1rem;
  }

  .banner-error {
    background: rgba(248,113,113,0.1);
    border: 1px solid rgba(248,113,113,0.2);
    color: #fca5a5;
    padding: 0.6rem 0.9rem;
    border-radius: 8px;
    font-size: 0.8rem;
    margin-bottom: 1rem;
  }

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
    display: flex;
    flex-direction: column;
  }
  .result-card:hover { transform: translateY(-3px); box-shadow: 0 8px 24px var(--shadow); }
  .result-card.discover { cursor: default; }
  .result-card.discover:hover { transform: none; box-shadow: none; }

  .poster-wrap { position: relative; }
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

  .type-pill {
    position: absolute;
    top: 0.4rem;
    left: 0.4rem;
    font-size: 0.6rem;
    padding: 0.15rem 0.45rem;
    border-radius: 10px;
    background: rgba(0,0,0,0.65);
    color: #fff;
    text-transform: uppercase;
    letter-spacing: 0.05em;
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

  .request-btn {
    margin-top: 0.5rem;
    width: 100%;
    background: var(--accent);
    color: #fff;
    border: none;
    border-radius: 6px;
    padding: 0.4rem 0.6rem;
    font-size: 0.72rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.12s;
  }
  .request-btn:hover:not(:disabled) { background: var(--accent-hover); }
  .request-btn:disabled { opacity: 0.5; cursor: not-allowed; }

  .status-pill {
    display: inline-block;
    margin-top: 0.5rem;
    font-size: 0.62rem;
    font-weight: 700;
    padding: 0.18rem 0.55rem;
    border-radius: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .status-pending      { background: rgba(251,191,36,0.15); color: #fcd34d; }
  .status-approved     { background: rgba(124,106,247,0.15); color: var(--accent-text); }
  .status-downloading  { background: rgba(96,165,250,0.15); color: #93c5fd; }
  .status-available    { background: rgba(52,211,153,0.15); color: #6ee7b7; }
  .status-declined     { background: rgba(248,113,113,0.15); color: #fca5a5; }
  .status-failed       { background: rgba(248,113,113,0.15); color: #fca5a5; }

  .result-card.skeleton {
    aspect-ratio: 2/3;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, #16161f 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.4s infinite;
    border: none;
    cursor: default;
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
