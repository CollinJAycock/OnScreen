<script lang="ts">
  // Search page with type-filter chips matching the web client
  // (Movies / TV Shows / Episodes / Tracks). Movies + TV Shows are on
  // by default; episodes + tracks default off so a search for "Dream"
  // doesn't bury three movie matches under fifty Pink Floyd tracks.
  // The filter set is persisted to localStorage under the same key
  // the web client uses, so a user who toggles episodes on once
  // doesn't have to do it again on every search.

  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, type SearchResult, Unauthorized } from '$lib/api';
  import OnScreenKeyboard from '$lib/components/OnScreenKeyboard.svelte';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import { openItem } from '$lib/nav';

  type Filters = { movie: boolean; show: boolean; episode: boolean; track: boolean };
  const FILTER_KEY = 'onscreen_search_filters';
  const defaults: Filters = { movie: true, show: true, episode: false, track: false };

  let filters = $state<Filters>({ ...defaults });
  let query = $state('');
  let results = $state<SearchResult[]>([]);
  let searching = $state(false);

  // album + artist + season piggyback on existing filters (album / artist
  // ride on the Track box; season rides on Show) — same rules the web
  // client uses, so the visible chips stay at four.
  const visibleResults = $derived(
    results.filter((r) => {
      switch (r.type) {
        case 'movie':
          return filters.movie;
        case 'show':
        case 'season':
          return filters.show;
        case 'episode':
          return filters.episode;
        case 'artist':
        case 'album':
        case 'track':
          return filters.track;
        default:
          return true; // unknown type — never hide
      }
    })
  );

  const hiddenCount = $derived(results.length - visibleResults.length);

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
        results = await endpoints.search.query(v.trim(), 30);
      } catch (e) {
        if (e instanceof Unauthorized) goto('/login');
      } finally {
        searching = false;
      }
    }, 250);
  }

  function toggle(key: keyof Filters) {
    filters = { ...filters, [key]: !filters[key] };
    persist();
  }

  function persist() {
    try {
      localStorage.setItem(FILTER_KEY, JSON.stringify(filters));
    } catch {
      // private mode / quota — saving is best-effort
    }
  }

  onMount(() => {
    try {
      const raw = localStorage.getItem(FILTER_KEY);
      if (raw) filters = { ...defaults, ...JSON.parse(raw) };
    } catch {
      // corrupt storage — fall back to defaults
    }
    return focusManager.pushBack(() => {
      goto('/hub');
      return true;
    });
  });
</script>

<div class="page">
  <h1>Search</h1>

  <div class="filter-row">
    <button
      use:focusable
      class="chip"
      class:on={filters.movie}
      onclick={() => toggle('movie')}>
      {filters.movie ? '✓ ' : ''}Movies
    </button>
    <button
      use:focusable
      class="chip"
      class:on={filters.show}
      onclick={() => toggle('show')}>
      {filters.show ? '✓ ' : ''}TV Shows
    </button>
    <button
      use:focusable
      class="chip"
      class:on={filters.episode}
      onclick={() => toggle('episode')}>
      {filters.episode ? '✓ ' : ''}Episodes
    </button>
    <button
      use:focusable
      class="chip"
      class:on={filters.track}
      onclick={() => toggle('track')}>
      {filters.track ? '✓ ' : ''}Tracks
    </button>
  </div>

  <OnScreenKeyboard value={query} onchange={onQueryChange} />

  <section class="results">
    {#if searching}
      <p class="status">Searching…</p>
    {:else if query.trim().length < 2}
      <p class="status">Type at least 2 characters.</p>
    {:else if results.length === 0}
      <p class="status">No results for "{query}"</p>
    {:else}
      {#if visibleResults.length === 0 && hiddenCount > 0}
        <p class="status">
          {hiddenCount} match{hiddenCount === 1 ? '' : 'es'} hidden by the type filters above.
        </p>
      {/if}
      <div class="grid">
        {#each visibleResults as r (r.id)}
          <PosterCard
            title={r.title}
            posterPath={r.poster_path ?? r.thumb_path}
            subtitle={r.year ? String(r.year) : r.type}
            onclick={() => openItem(r.id, r.type)}
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
    gap: 24px;
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 0;
  }
  .filter-row {
    display: flex;
    gap: 12px;
    flex-wrap: wrap;
  }
  .chip {
    padding: 12px 24px;
    border-radius: 999px;
    border: 1px solid rgba(255, 255, 255, 0.27);
    background: rgba(255, 255, 255, 0.13);
    color: var(--text-secondary);
    font-size: var(--font-sm);
    font-family: inherit;
    cursor: pointer;
  }
  .chip.on {
    background: var(--accent, #7c6af7);
    color: #fff;
    border-color: var(--accent, #7c6af7);
  }
  .chip:focus-visible {
    outline: none;
    border-color: #fff;
    transform: scale(1.05);
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
