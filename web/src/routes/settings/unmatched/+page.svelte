<script lang="ts">
  // Admin "Fix Match" tray. Lists every top-level movie/show with no
  // provider IDs (tmdb / tvdb / imdb) and lets the operator resolve
  // each one by searching TMDB and applying the right ID. Per-row
  // expand → search-as-you-type → click candidate → match applied →
  // row disappears. The match endpoint already does the merge-on-
  // existing-canonical pre-flight server-side, so picking a TMDB ID
  // that another row in the library is already attached to merges
  // children + deletes the loser cleanly.

  import { onMount } from 'svelte';
  import {
    unmatchedApi,
    libraryApi,
    itemApi,
    type UnmatchedItem,
    type Library,
    type MatchCandidate,
  } from '$lib/api';
  import { toast } from '$lib/stores/toast';

  let loading = true;
  let error = '';
  let items: UnmatchedItem[] = [];
  let libraries: Record<string, Library> = {};

  // Per-row expand state. Keyed by item.id; only one row open at a
  // time keeps the page scannable when there are dozens of items.
  let openId: string | null = null;
  let query = '';
  let yearInput = ''; // optional release-year filter, prefilled from the item
  let searching = false;
  let searchError = '';
  let candidates: MatchCandidate[] = [];
  let applyingTmdbId: number | null = null;

  onMount(async () => {
    try {
      const [u, libs] = await Promise.all([
        unmatchedApi.list(),
        libraryApi.list(),
      ]);
      items = u.items;
      libraries = Object.fromEntries(libs.map((l) => [l.id, l]));
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load unmatched items';
    } finally {
      loading = false;
    }
  });

  function libraryName(id: string): string {
    return libraries[id]?.name ?? id.slice(0, 8);
  }

  async function openRow(item: UnmatchedItem) {
    if (openId === item.id) {
      // Toggle closed.
      openId = null;
      candidates = [];
      query = '';
      return;
    }
    openId = item.id;
    query = item.title;
    // Prefill the year filter from the item — for generic titles ("The
    // Dead") the right film often never cracks TMDB's popularity-ranked
    // top 10 without it. Clearing the box searches all years.
    yearInput = item.year ? String(item.year) : '';
    candidates = [];
    searchError = '';
    await runSearch(item.id);
  }

  function parsedYear(): number | undefined {
    const y = parseInt(yearInput, 10);
    return Number.isInteger(y) && y >= 1870 && y <= 2100 ? y : undefined;
  }

  async function runSearch(itemId: string) {
    if (!query.trim()) {
      candidates = [];
      return;
    }
    searching = true;
    searchError = '';
    try {
      candidates = await itemApi.searchMatch(itemId, query.trim(), parsedYear());
    } catch (e: unknown) {
      searchError = e instanceof Error ? e.message : 'Search failed';
      candidates = [];
    } finally {
      searching = false;
    }
  }

  // Debounce keystroke-driven searches so we don't hammer TMDB.
  let searchTimer: ReturnType<typeof setTimeout> | null = null;
  function onQueryInput(itemId: string) {
    if (searchTimer) clearTimeout(searchTimer);
    searchTimer = setTimeout(() => runSearch(itemId), 350);
  }

  async function applyMatch(item: UnmatchedItem, c: MatchCandidate) {
    applyingTmdbId = c.tmdb_id;
    try {
      await itemApi.applyMatch(item.id, c.tmdb_id);
      toast.success(`Matched “${item.title}” → ${c.title}${c.year ? ` (${c.year})` : ''}`);
      // Remove the row locally so the user sees the list shrink without a refetch.
      items = items.filter((i) => i.id !== item.id);
      openId = null;
      candidates = [];
      query = '';
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Apply failed';
      toast.error(msg);
    } finally {
      applyingTmdbId = null;
    }
  }
</script>

<svelte:head><title>Fix Match — OnScreen</title></svelte:head>

<div class="page">
  <header class="head">
    <h1>Fix Match</h1>
    <p class="sub">
      Items the scanner couldn't auto-match against TMDB. Pick the right
      result to attach IDs and pull metadata + artwork. The match also
      merges into an existing canonical row if you pick a TMDB ID that
      another item in the library already uses.
    </p>
  </header>

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if error}
    <p class="error">{error}</p>
  {:else if items.length === 0}
    <p class="muted empty">All movies and shows are matched. Nothing to do here. ✅</p>
  {:else}
    <p class="count">{items.length} {items.length === 1 ? 'item needs' : 'items need'} a match</p>

    <ul class="rows">
      {#each items as item (item.id)}
        <li class="row" class:open={openId === item.id}>
          <button type="button" class="row-head" on:click={() => openRow(item)}>
            <span class="type-pill type-{item.type}">{item.type}</span>
            <span class="title">{item.title}</span>
            {#if item.year}<span class="year">({item.year})</span>{/if}
            <span class="lib">{libraryName(item.library_id)}</span>
            <span class="chevron">{openId === item.id ? '▾' : '▸'}</span>
          </button>

          {#if openId === item.id}
            <div class="picker">
              <div class="search-row">
                <input
                  type="text"
                  class="search"
                  placeholder="Search TMDB…"
                  bind:value={query}
                  on:input={() => onQueryInput(item.id)}
                  on:keydown={(e) => { if (e.key === 'Enter') runSearch(item.id); }}
                />
                <input
                  type="text"
                  class="search year-input"
                  placeholder="Year"
                  inputmode="numeric"
                  maxlength="4"
                  title="Release year filter — clear to search all years"
                  bind:value={yearInput}
                  on:input={() => onQueryInput(item.id)}
                  on:keydown={(e) => { if (e.key === 'Enter') runSearch(item.id); }}
                />
              </div>

              {#if searching}
                <p class="muted">Searching…</p>
              {:else if searchError}
                <p class="error">{searchError}</p>
              {:else if candidates.length === 0}
                <p class="muted">No results — try a different query.</p>
              {:else}
                <ul class="cands">
                  {#each candidates as c (c.tmdb_id)}
                    <li>
                      <button
                        type="button"
                        class="cand"
                        disabled={applyingTmdbId !== null}
                        on:click={() => applyMatch(item, c)}
                      >
                        {#if c.poster_url}
                          <img src={c.poster_url} alt="" class="cand-poster" loading="lazy" />
                        {:else}
                          <div class="cand-poster placeholder"></div>
                        {/if}
                        <div class="cand-text">
                          <div class="cand-title">
                            {c.title}{#if c.year} <span class="cand-year">({c.year})</span>{/if}
                            {#if applyingTmdbId === c.tmdb_id}<span class="applying">Applying…</span>{/if}
                          </div>
                          {#if c.summary}
                            <div class="cand-summary">{c.summary}</div>
                          {/if}
                        </div>
                      </button>
                    </li>
                  {/each}
                </ul>
              {/if}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .page { max-width: 920px; margin: 0 auto; padding: 1.5rem; }
  .head { margin-bottom: 1.5rem; }
  h1 { font-size: 1.4rem; margin: 0 0 0.5rem; }
  .sub { color: var(--text-muted); font-size: 0.85rem; line-height: 1.5; margin: 0; max-width: 720px; }
  .muted { color: var(--text-muted); font-size: 0.9rem; }
  .empty { padding: 3rem 1rem; text-align: center; }
  .error { color: var(--error, #f08080); font-size: 0.9rem; }
  .count { color: var(--text-muted); font-size: 0.85rem; margin-bottom: 0.75rem; }

  .rows { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.4rem; }
  .row {
    background: var(--bg-surface, #14141e);
    border: 1px solid var(--border, rgba(255,255,255,0.08));
    border-radius: 8px;
    overflow: hidden;
  }
  .row.open { border-color: rgba(124,106,247,0.4); }

  .row-head {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.75rem 1rem;
    background: transparent;
    border: none;
    color: inherit;
    text-align: left;
    cursor: pointer;
    font-size: 0.9rem;
  }
  .row-head:hover { background: var(--bg-hover, rgba(255,255,255,0.04)); }

  .type-pill {
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.15rem 0.5rem;
    border-radius: 4px;
    background: var(--bg-hover);
    color: var(--text-muted);
    flex-shrink: 0;
  }
  .type-show { color: #4f8df0; }
  .type-movie { color: #f0c14f; }

  .title { flex: 1; font-weight: 500; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .year { color: var(--text-muted); font-size: 0.8rem; flex-shrink: 0; }
  .lib { color: var(--text-muted); font-size: 0.75rem; flex-shrink: 0; }
  .chevron { color: var(--text-muted); flex-shrink: 0; }

  .picker { padding: 0.75rem 1rem 1rem; border-top: 1px solid var(--border); }
  .search-row { display: flex; gap: 0.5rem; margin-bottom: 0.75rem; }
  .search {
    width: 100%;
    padding: 0.55rem 0.75rem;
    background: var(--bg-primary);
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    color: var(--text-primary);
    font-size: 0.9rem;
    outline: none;
    box-sizing: border-box;
  }
  .year-input { width: 5.5rem; flex-shrink: 0; text-align: center; }
  .search:focus { border-color: rgba(124,106,247,0.5); box-shadow: 0 0 0 3px var(--accent-bg); }

  .cands { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.4rem; }
  .cand {
    width: 100%;
    display: flex;
    gap: 0.75rem;
    padding: 0.5rem;
    background: var(--bg-primary);
    border: 1px solid var(--border);
    border-radius: 6px;
    cursor: pointer;
    text-align: left;
    color: inherit;
  }
  .cand:hover:not(:disabled) { border-color: rgba(124,106,247,0.4); }
  .cand:disabled { opacity: 0.5; cursor: progress; }
  .cand-poster { width: 60px; height: 90px; object-fit: cover; border-radius: 4px; flex-shrink: 0; background: var(--bg-hover); }
  .cand-poster.placeholder { background: var(--bg-hover); }
  .cand-text { flex: 1; min-width: 0; }
  .cand-title { font-size: 0.9rem; font-weight: 500; }
  .cand-year { color: var(--text-muted); font-weight: 400; }
  .cand-summary { color: var(--text-muted); font-size: 0.8rem; margin-top: 0.25rem; line-height: 1.4; display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; }
  .applying { margin-left: 0.5rem; color: var(--accent-text); font-size: 0.75rem; }
</style>
