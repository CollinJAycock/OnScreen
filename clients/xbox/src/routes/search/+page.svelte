<script lang="ts">
  // Search page with type-filter chips matching the web client
  // (Movies / TV Shows / Episodes / Tracks). Movies + TV Shows are on
  // by default; episodes + tracks default off so a search for "Dream"
  // doesn't bury three movie matches under fifty Pink Floyd tracks.
  // The filter set is persisted to localStorage under the same key
  // the web client uses, so a user who toggles episodes on once
  // doesn't have to do it again on every search.
  //
  // A "Search in" chip ahead of them narrows the search to one library
  // (Android's scope chip; the server's library_id). Like Android's, the
  // choice lasts as long as the screen (including a trip into a result and
  // Back, see $lib/searchMemory).

  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { endpoints, type Library, type SearchResult, Unauthorized } from '$lib/api';
  import OnScreenKeyboard from '$lib/components/OnScreenKeyboard.svelte';
  import OptionsDialog from '$lib/components/OptionsDialog.svelte';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import { focusFirstOf, restoreGuard, restoreKeyed, takeFocusMemo } from '$lib/focus/memory';
  import { latestOnly } from '$lib/latest';
  import { openItem } from '$lib/nav';
  import { lastSearch, rememberSearch } from '$lib/searchMemory';

  type Filters = { movie: boolean; show: boolean; episode: boolean; track: boolean };
  const FILTER_KEY = 'onscreen_search_filters';
  const defaults: Filters = { movie: true, show: true, episode: false, track: false };

  let filters = $state<Filters>({ ...defaults });
  let query = $state('');
  let results = $state<SearchResult[]>([]);
  let searching = $state(false);
  // The newest search failed: its results are gone (they'd be an older
  // query's, or another scope's), and the page says so.
  let searchFailed = $state(false);

  // Library scope: null searches everything. The chip shows once the
  // library list has loaded (and isn't empty), as on Android.
  let libraries = $state<Library[]>([]);
  let scope = $state<Library | null>(null);
  let scopeMenuOpen = $state(false);
  let scopeOrigin: HTMLElement | null = null;

  // Typing and a scope change both start searches; only the newest one's
  // answer is shown, so a slow answer for "Dre" can't replace "Dream"'s.
  const searches = latestOnly();

  // What Back should bring back ($lib/searchMemory). The previous one is
  // read here, at creation, before this effect's first run overwrites it.
  const previousSearch = lastSearch();
  $effect(() => {
    rememberSearch({ query, scope });
  });

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
      // Nothing to show, and nothing still on its way may land on top.
      searches.supersede();
      results = [];
      searching = false;
      searchFailed = false;
      return;
    }
    debounce = setTimeout(() => void runSearch(), 250);
  }

  /** Search for the current query and scope. True when its results were
   *  applied (not superseded by a newer search, and no failure). */
  async function runSearch(): Promise<boolean> {
    const q = query.trim();
    if (q.length < 2) return false;
    searching = true;
    try {
      // A stale search leaves `searching` to the newer one still out.
      const applied = await searches(endpoints.search.query(q, 30, scope?.id), (r) => {
        results = r;
        searchFailed = false;
      });
      if (applied) searching = false;
      return applied;
    } catch (e) {
      // Only the newest search's failure gets here (latestOnly).
      searching = false;
      results = [];
      searchFailed = true;
      if (e instanceof Unauthorized) goto('#/login');
      return false;
    }
  }

  const scopeLabel = $derived(`Search in: ${scope?.name ?? 'All libraries'}`);

  function openScopeMenu() {
    scopeOrigin = focusManager.currentElement();
    scopeMenuOpen = true;
  }

  async function closeScopeMenu() {
    scopeMenuOpen = false;
    await tick();
    if (scopeOrigin && document.body.contains(scopeOrigin)) focusManager.focus(scopeOrigin);
    else focusManager.refocus();
    scopeOrigin = null;
  }

  /** Narrow to `lib` (null = every library) and search again at once. The
   *  old scope's results go straight away: they'd show under the new chip
   *  until the answer lands. Picking the same scope after a failed search
   *  tries it again. */
  function pickScope(lib: Library | null) {
    const changed = (lib?.id ?? null) !== (scope?.id ?? null);
    scope = lib;
    void closeScopeMenu();
    if (!changed && !searchFailed) return;
    if (changed) results = [];
    if (debounce) clearTimeout(debounce);
    void runSearch();
  }

  // The current scope carries a check, like the library page's menus, and
  // the menu opens on it (a library gone from the list: on All libraries).
  const scopeOptions = $derived([
    { label: `${scope === null ? '✓ ' : ''}All libraries`, onselect: () => pickScope(null) },
    ...libraries.map((l) => ({
      label: `${scope?.id === l.id ? '✓ ' : ''}${l.name}`,
      onselect: () => pickScope(l),
    })),
  ]);
  const scopeIndex = $derived(scope ? libraries.findIndex((l) => l.id === scope?.id) + 1 : 0);

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
    // Back from a result: the same search again, then focus on the result
    // (the first one when it has gone). Ends early if the user moves focus
    // or leaves first (lib/focus/memory). Any other arrival (the top nav),
    // or a search that didn't come back, starts on the keyboard as
    // Android's search does: the pill that opened the page is gone with
    // the last page, and with nothing focused the first press only
    // brought the ring up, on the nav's Home.
    const memo = takeFocusMemo();
    const guard = restoreGuard();
    const KEYBOARD = '.osk [data-focusable]';
    if (memo) {
      query = previousSearch.query;
      scope = previousSearch.scope;
      (async () => {
        const applied = await runSearch();
        await tick();
        if (!applied || !memo.focusedId) focusFirstOf(KEYBOARD, guard);
        else if (!restoreKeyed(memo, guard)) focusFirstOf('.grid [data-focusable]', guard);
      })();
    } else {
      void tick().then(() => focusFirstOf(KEYBOARD, guard));
    }
    (async () => {
      try {
        const libs = await endpoints.libraries.list();
        libraries = Array.isArray(libs) ? libs : [];
      } catch {
        // Best effort: without the list there's no chip, and the search
        // covers every library as before.
      }
    })();
    const offBack = focusManager.pushBack(() => {
      goto('#/hub');
      return true;
    });
    return () => {
      guard.end();
      offBack();
    };
  });
</script>

<div class="page">
  <TopNav />
  <h1>Search</h1>

  <!-- data-focus-row: Left / Right stay among the chips, so Right from
       the last one stops there instead of dropping to whatever lies right
       of it further down (a key, a result). -->
  <div class="filter-row" data-focus-row>
    {#if libraries.length > 0}
      <button use:focusable class="chip" class:on={scope !== null} onclick={openScopeMenu}>
        {scopeLabel}
      </button>
      <span class="divider" aria-hidden="true"></span>
    {/if}
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

  <!-- Exitable: it has no done key, and Down from its last row is how the
       remote reaches the results (Android's search does the same); Up from
       its top row reaches the chips. Left / Right stay in the keyboard. -->
  <OnScreenKeyboard value={query} onchange={onQueryChange} exitable />

  <section class="results">
    {#if searching}
      <p class="status">Searching…</p>
    {:else if query.trim().length < 2}
      <p class="status">Type at least 2 characters.</p>
    {:else if searchFailed}
      <p class="status">Couldn't search. Try again.</p>
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
            focusKey={r.id}
            onclick={() => openItem(r.id, r.type)}
          />
        {/each}
      </div>
    {/if}
  </section>
</div>

{#if scopeMenuOpen}
  <OptionsDialog
    title="Search in"
    options={scopeOptions}
    initialIndex={scopeIndex}
    oncancel={() => void closeScopeMenu()}
  />
{/if}

<style>
  /* A one-column grid rather than a flex column: grid `gap` works on
     webOS 6's Chromium 79, flexbox `gap` needs Chrome 84. */
  .page {
    padding: 0 var(--page-pad) var(--page-pad);
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    align-content: start;
    row-gap: 24px;
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 24px 0 0;
  }
  /* The chips wrap. Each child carries a top + right margin and the row
     pulls itself up and right by the same, spacing them like `gap: 12px`
     (flexbox gap is Chrome 84; webOS 6 runs 79). */
  .filter-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    margin: -12px -12px 0 0;
  }
  .filter-row > * {
    margin: 12px 12px 0 0;
  }
  /* Between the scope chip and the type chips, spaced like the library
     page's (24px either side). */
  .filter-row > .divider {
    width: 2px;
    height: 40px;
    margin-left: 12px;
    margin-right: 24px;
    background: var(--border-strong);
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
  .chip:focus {
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
