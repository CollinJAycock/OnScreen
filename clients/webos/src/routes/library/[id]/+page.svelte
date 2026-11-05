<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import {
    endpoints,
    Unauthorized,
    type LetterJump,
    type MediaItem,
    type Library,
    type WatchFilter,
  } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import type { RemoteKey } from '$lib/focus/keys';
  import { focusManager } from '$lib/focus/manager';
  import {
    focusFirstOf,
    restoreFocusTo,
    restoreGuard,
    restoreKeyed,
    takeFocusMemo,
    type FocusMemo,
    type RestoreGuard,
  } from '$lib/focus/memory';
  import LibrarySearch from '$lib/components/LibrarySearch.svelte';
  import OptionsDialog from '$lib/components/OptionsDialog.svelte';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
  import { latestOnly } from '$lib/latest';
  import { letterAt, loadLetters, planJump, railSort } from '$lib/letterRail';
  import { openItem, goBack } from '$lib/nav';
  import { Pager, nearEnd, refill, uniqueBy } from '$lib/paging';
  import {
    SORT_OPTIONS,
    defaultSortFor,
    parseSort,
    sameSort,
    sortKey,
    sortLabel,
    type LibrarySort,
  } from '$lib/librarySort';
  import {
    WATCH_FILTER_OPTIONS,
    cardWatchBadge,
    endpointMissing,
    errorStatus,
    hasWatchFields,
    parseWatchFilter,
    supportsWatchState,
  } from '$lib/watchState';

  // Six posters a row (see .grid). The grid pages 100 at a time and asks for
  // the next page once focus reaches the last two rows, so a big library
  // (or photo library) scrolls past what one request returns. The server
  // caps a page at 200.
  const COLUMNS = 6;
  const PAGE_SIZE = 100;

  // $state.raw: thousands of posters don't need a deep proxy, and the pager
  // hands out a new array on every page.
  let items = $state.raw<MediaItem[] | null>(null);
  let library = $state<Library | null>(null);
  let error = $state('');
  let loadingMore = $state(false);
  // The first load settled: the filter bar mounts after it, so its
  // autofocus picks reflect what actually loaded.
  let ready = $state(false);

  const libraryID = $derived(page.params.id!);

  // Pages for the current sort / genre / watch filter. A query change resets
  // it; a page that was still in flight is dropped (generation guard).
  const pager = new Pager<MediaItem>((i) => i.id, PAGE_SIZE);

  // Sort + genre (every library type, like Android's Sort & filter).
  let sort = $state<LibrarySort>(defaultSortFor(null));
  let genre = $state('');
  let genres = $state<string[]>([]);
  let menu = $state<'sort' | 'genre' | null>(null);
  let menuOrigin: HTMLElement | null = null;

  // Watch filter + Surprise me (v2.5). Offered for video libraries on a
  // server whose listing carries watch state; older servers ignore ?watch=
  // and have no /random, so they keep the plain grid.
  let watch = $state<WatchFilter | ''>('');
  let watchAware = $state(false);
  let surpriseMissing = $state(false);
  let surprising = $state(false);
  let notice = $state('');
  // Cards autofocus on the first load only; a watch-filter change keeps
  // focus on the chip the user just pressed.
  let firstLoad = $state(true);
  // Back from an item: the first card holds its autofocus while the grid
  // refills and the card the user opened takes focus again.
  let restoring = $state(false);

  // The A–Z rail beside the grid ($lib/letterRail), in title order on a
  // server with the index. A letter opens the grid at its first item
  // (Pager.startAt) instead of paging through every row before it, so the
  // grid can start part way down the listing: `windowStart` is the row its
  // first card is (it lines up in its column, see .lead), and the rows above
  // come in a page at a time as focus goes up (loadEarlier).
  let letters = $state<LetterJump[]>([]);
  const letterLoads = latestOnly();
  let windowStart = $state(0);
  // The listing row of the card with focus (-1: none yet): the rail marks its
  // letter, and Left from the grid lands on it.
  let focusedRow = $state(-1);
  let loadingBefore = $state(false);
  let earlier: Promise<boolean> | null = null;
  // Each jump's number: one overtaken by a newer jump doesn't move focus.
  let jumps = 0;
  const showRail = $derived(railSort(sort) && letters.length > 0);
  const railIndex = $derived(focusedRow >= 0 ? letterAt(letters, focusedRow) : -1);
  const lead = $derived(windowStart % COLUMNS);

  // "Search this library": a panel over the page (LibrarySearch). Back from
  // a result reopens it on the same text, kept per library for the session
  // like the filters.
  let searchOpen = $state(false);
  let searchText = $state('');
  let searchMemo = $state.raw<FocusMemo | null>(null);

  const showWatchControls = $derived(watchAware && supportsWatchState(library?.type));
  // The genre chip needs genres to offer (a photo library has none) — or a
  // stored genre the user must be able to clear.
  const showGenre = $derived(genres.length > 0 || genre !== '');
  const filtered = $derived(watch !== '' || genre !== '');

  // Sort, genre and watch filter survive a trip into an item and Back (the
  // page remounts), per library, for the session.
  const filterKey = $derived(`onscreen.watch_filter.${libraryID}`);
  const sortStoreKey = $derived(`onscreen.library_sort.${libraryID}`);
  const genreStoreKey = $derived(`onscreen.genre_filter.${libraryID}`);
  // Where a letter opened the grid, and the open search's text: Back from an
  // item comes back to them (any other arrival starts at the top).
  const startStoreKey = $derived(`onscreen.library_start.${libraryID}`);
  const searchStoreKey = $derived(`onscreen.library_search.${libraryID}`);
  function readStored(key: string): string | null {
    try {
      return sessionStorage.getItem(key);
    } catch {
      return null;
    }
  }
  function writeStored(key: string, value: string) {
    try {
      if (value) sessionStorage.setItem(key, value);
      else sessionStorage.removeItem(key);
    } catch {
      /* storage unavailable — the choice just resets on return */
    }
  }

  onMount(() => {
    const memo = takeFocusMemo();
    restoring = !!memo?.focusedId;
    // Ends the restore when the page goes away or the user moves focus first.
    const guard = restoreGuard();
    watch = parseWatchFilter(readStored(filterKey));
    genre = readStored(genreStoreKey) ?? '';
    const storedSort = parseSort(readStored(sortStoreKey));
    // Only Back from an item returns to a letter or an open search.
    const storedStart = memo ? Number(readStored(startStoreKey)) || 0 : 0;
    const storedSearch = memo ? (readStored(searchStoreKey) ?? '') : '';
    if (!memo) {
      writeStored(startStoreKey, '');
      writeStored(searchStoreKey, '');
    }
    if (storedSearch) {
      // Back from a search result: the panel again, which puts focus back on
      // the result; the grid loads under it without taking focus.
      searchText = storedSearch;
      searchMemo = memo;
      searchOpen = true;
    }
    (async () => {
      try {
        // The library's type picks the default sort (date-driven types open
        // newest first), so it's needed before the first page. list-by-id
        // isn't exposed; the library list is small.
        const libs = await endpoints.libraries.list();
        library = libs.find((l) => l.id === libraryID) ?? null;
        sort = storedSort ?? defaultSortFor(library?.type);
        if (storedStart > 0 && railSort(sort)) pager.startAt(storedStart);
        void loadGenres();
        void loadRail();
        await loadMore();
        // A letter's start past the end now (titles removed): the top.
        if (items !== null && items.length === 0 && pager.start > 0) {
          pager.reset();
          writeStored(startStoreKey, '');
          await loadMore();
        }
        // Failed (the page shows the error) or signed out (on its way to login).
        if (items === null) return;
        const first: MediaItem[] = items;
        // An empty answer to a stored filter is a v2.5 server with no
        // match (an older one ignores ?watch= and lists everything).
        watchAware = hasWatchFields(first) || (watch !== '' && first.length === 0);
        if (!watchAware && watch) {
          // Stored filter on a server that doesn't know it — show everything.
          watch = '';
          writeStored(filterKey, '');
        }
        ready = true;
        await tick();
        if (memo && restoring && !searchOpen) await restoreGrid(memo, guard);
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        else error = (e as Error).message;
      } finally {
        firstLoad = false;
        restoring = false;
      }
    })();

    const offBack = focusManager.pushBack(() => {
      goBack();
      return true;
    });
    const offKeys = focusManager.pushKeyHandler(onKey);
    return () => {
      guard.end();
      offBack();
      offKeys();
    };
  });

  async function loadGenres() {
    try {
      const rows = await endpoints.libraries.genres(libraryID);
      genres = uniqueBy(
        rows.map((g) => g.name).filter((n) => typeof n === 'string' && n.trim() !== ''),
        (n) => n,
      );
    } catch {
      // Best effort: no genres (or an older server) just hides the chip.
    }
  }

  /** The next page for the current query. Initial-load failures surface as
   *  the page error; a failed later page keeps the grid, and the next focus
   *  near the end retries it. True when the page changed the grid. */
  async function loadMore(): Promise<boolean> {
    const q = { sort: sort.sort, dir: sort.dir, watch, genre };
    loadingMore = true;
    try {
      const changed = await pager.loadMore((offset, limit) =>
        endpoints.libraries.listItems(libraryID, q.sort, q.dir, q.watch, q.genre, limit, offset),
      );
      if (changed) showPager();
      return changed;
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
      else if (items === null) error = (e as Error).message;
      return false;
    } finally {
      loadingMore = pager.loading;
    }
  }

  // Back from an item: fetch again the pages the grid had (the card can be
  // far down a big library; the sort and filters were restored above), and
  // put focus back on the card as soon as its page is in. The first card
  // takes it when the card never shows up (gone from the library, or past
  // refill's page cap). All of it stops once `guard` does: the user left
  // the page (its cards would be the next page's) or moved focus before the
  // card came in, and no further page is fetched for it either (focus near
  // the end still pages as usual).
  async function restoreGrid(memo: FocusMemo, guard: RestoreGuard<HTMLElement>) {
    let found = restoreKeyed(memo, guard);
    const loadWhileRestoring = () => (guard.active ? loadMore() : Promise.resolve(false));
    await refill(pager, loadWhileRestoring, memo.loadedCount, async () => {
      if (found) return;
      await tick();
      found = restoreKeyed(memo, guard);
    });
    if (!found) focusFirstOf('.grid [data-focusable]', guard);
  }

  /** The grid as the pager has it. */
  function showPager() {
    items = pager.items;
    windowStart = pager.start;
  }

  function onCardFocus(index: number) {
    focusedRow = pager.start + index;
    if (!pager.done && nearEnd(index, items?.length ?? 0, COLUMNS)) void loadMore();
    // Opened at a letter: the page above comes in as focus nears the top, so
    // Up from the first row usually finds it there already. Not for the
    // letter's own first card, which the jump just focused.
    if (pager.hasBefore && index > 0 && index < COLUMNS * 2) void loadEarlier();
  }

  /** The grid's card `index` (in `items`), if it's on the page. */
  function cardAt(index: number): HTMLElement | null {
    if (index < 0) return null;
    return document.querySelectorAll<HTMLElement>('.browse .grid [data-focusable]')[index] ?? null;
  }

  /** The page above the grid's first card, put in front of it (one load at a
   *  time: a second call shares the first's). True once it's in. */
  function loadEarlier(): Promise<boolean> {
    if (!earlier) {
      earlier = prependEarlier().finally(() => {
        earlier = null;
      });
    }
    return earlier;
  }

  async function prependEarlier(): Promise<boolean> {
    const q = { sort: sort.sort, dir: sort.dir, watch, genre };
    loadingBefore = true;
    try {
      const changed = await pager.loadBefore((offset, limit) =>
        endpoints.libraries.listItems(libraryID, q.sort, q.dir, q.watch, q.genre, limit, offset),
      );
      if (!changed) return false;
      // The rows come in above what's on screen: the focused card stays where
      // it is, the page scrolled down by what was put above it.
      const el = focusManager.currentElement();
      const before = el ? el.getBoundingClientRect().top : 0;
      showPager();
      writeStored(startStoreKey, String(pager.start));
      await tick();
      const root = document.querySelector<HTMLElement>('main.tv-root');
      if (el && root && document.body.contains(el)) {
        const moved = el.getBoundingClientRect().top - before;
        if (moved !== 0) root.scrollTop += moved;
      }
      return true;
    } catch (e) {
      // The next move up tries again.
      if (e instanceof Unauthorized) goto('#/login');
      return false;
    } finally {
      loadingBefore = pager.loadingBefore;
    }
  }

  /** The rail's letters for the current sort and filters (none when not in
   *  title order, or on a server without the index). */
  async function loadRail() {
    const s = { sort: sort.sort, dir: sort.dir };
    const q = { watch, genre };
    await letterLoads(
      loadLetters(s, () => endpoints.libraries.letters(libraryID, s.sort, s.dir, q.watch, q.genre)),
      (rows) => {
        letters = rows;
      },
    );
  }

  /** A letter's OK: its first item takes focus, the grid opened there when
   *  that row isn't loaded (no row before it is fetched). */
  async function jumpToLetter(letter: LetterJump, origin: HTMLElement) {
    const mine = ++jumps;
    const plan = planJump(letter, pager.start, items?.length ?? 0);
    if (plan.kind === 'focus') {
      const card = cardAt(plan.index);
      if (card) restoreFocusTo(card);
      return;
    }
    pager.startAt(plan.offset);
    writeStored(startStoreKey, String(plan.offset));
    items = null;
    windowStart = plan.offset;
    error = '';
    notice = '';
    await loadMore();
    await tick();
    // Overtaken by another letter, or the user moved on while it loaded.
    if (mine !== jumps || focusManager.currentElement() !== origin) return;
    const card = cardAt(0);
    if (card) restoreFocusTo(card);
  }

  /**
   * The rail's D-pad rules, ahead of the spatial move. Left from the grid's
   * first column goes to the focused card's letter (not to whichever letter
   * is level with it); Up from the first loaded row of a grid opened at a
   * letter waits for the page above and moves into it, rather than leaving
   * the grid for the chips. In the rail, Up from the top goes to the Search
   * chip and Down from the bottom stays put (the grid's cards lie below).
   */
  function onKey(k: RemoteKey): boolean {
    const el = focusManager.currentElement();
    if (!el || !el.closest('.browse') || menu || searchOpen) return false;
    if (el.closest('.rail')) {
      const buttons = [...document.querySelectorAll<HTMLElement>('.rail [data-focusable]')];
      const at = buttons.indexOf(el);
      if (k === 'down') return at === buttons.length - 1;
      if (k === 'up' && at === 0) {
        const chip = document.querySelector<HTMLElement>('[data-focus-key="chip:search"]');
        if (chip) focusManager.focus(chip);
        return !!chip;
      }
      return false;
    }
    if (!items || focusedRow < 0 || !el.closest('.grid')) return false;
    const index = focusedRow - pager.start;
    if (k === 'left' && showRail && (index === 0 || focusedRow % COLUMNS === 0)) {
      const buttons = document.querySelectorAll<HTMLElement>('.rail [data-focusable]');
      const target = buttons[Math.max(0, railIndex)];
      if (!target) return false;
      focusManager.focus(target);
      return true;
    }
    if (k === 'up' && pager.hasBefore && focusedRow - COLUMNS < pager.start) {
      const row = focusedRow;
      void loadEarlier().then(async (loaded) => {
        if (!loaded || focusManager.currentElement() !== el) return;
        await tick();
        const above = cardAt(row - COLUMNS - pager.start);
        if (above) focusManager.focus(above);
      });
      return true;
    }
    return false;
  }

  function openSearch() {
    searchText = '';
    searchMemo = null;
    searchOpen = true;
  }

  async function closeSearch() {
    searchOpen = false;
    writeStored(searchStoreKey, '');
    await tick();
    const chip = document.querySelector<HTMLElement>('[data-focus-key="chip:search"]');
    if (chip) focusManager.focus(chip);
    else focusManager.refocus();
  }

  /** Start the grid over for a changed sort / genre / watch filter. With
   *  `focusFirst` the first card takes focus (Android jumps there after a
   *  sort or genre change); an empty result keeps focus on the bar. */
  async function reload(focusFirst: boolean) {
    pager.reset();
    items = null;
    windowStart = 0;
    focusedRow = -1;
    writeStored(startStoreKey, '');
    error = '';
    notice = '';
    // The letters' offsets are the old listing's: none until the new ones.
    letters = [];
    void loadRail();
    await loadMore();
    if (!focusFirst) return;
    await tick();
    const card = document.querySelector<HTMLElement>('.grid [data-focusable]');
    const fallback = menuOrigin && document.body.contains(menuOrigin) ? menuOrigin : null;
    menuOrigin = null;
    if (card) focusManager.focus(card);
    else if (fallback) focusManager.focus(fallback);
    else focusManager.refocus();
  }

  async function setWatch(next: WatchFilter | '') {
    if (next === watch && items) return;
    watch = next;
    writeStored(filterKey, next);
    await reload(false);
  }

  function openMenu(which: 'sort' | 'genre') {
    menuOrigin = focusManager.currentElement();
    menu = which;
  }

  async function closeMenu() {
    menu = null;
    await tick();
    if (menuOrigin && document.body.contains(menuOrigin)) focusManager.focus(menuOrigin);
    else focusManager.refocus();
    menuOrigin = null;
  }

  function pickSort(next: LibrarySort) {
    if (sameSort(next, sort)) {
      void closeMenu();
      return;
    }
    menu = null;
    sort = { sort: next.sort, dir: next.dir };
    writeStored(sortStoreKey, sortKey(sort));
    void reload(true);
  }

  function pickGenre(next: string) {
    if (next === genre) {
      void closeMenu();
      return;
    }
    menu = null;
    genre = next;
    writeStored(genreStoreKey, next);
    void reload(true);
  }

  // The options for the open menu; the current choice carries a check, and
  // the menu opens on it (`at`).
  const menuChoice = $derived.by(() => {
    if (menu === 'sort') {
      return {
        options: SORT_OPTIONS.map((o) => ({
          label: `${sameSort(o, sort) ? '✓ ' : ''}${o.label}`,
          onselect: () => pickSort(o),
        })),
        at: Math.max(0, SORT_OPTIONS.findIndex((o) => sameSort(o, sort))),
      };
    }
    if (menu === 'genre') {
      const names = genre && !genres.includes(genre) ? [genre, ...genres] : genres;
      return {
        options: [
          { label: `${genre === '' ? '✓ ' : ''}All genres`, onselect: () => pickGenre('') },
          ...names.map((g) => ({ label: `${g === genre ? '✓ ' : ''}${g}`, onselect: () => pickGenre(g) })),
        ],
        at: genre === '' ? 0 : names.indexOf(genre) + 1,
      };
    }
    return { options: [], at: 0 };
  });

  async function surprise() {
    if (surprising) return;
    surprising = true;
    notice = '';
    try {
      // The current filters apply: a random unwatched comedy, not any title.
      const pick = await endpoints.libraries.random(libraryID, watch, genre);
      openItem(pick.id, pick.type);
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
      else if (errorStatus(e) === 404 && (e as { code?: string }).code === 'NOT_FOUND') {
        notice = 'Nothing matches the current filters.';
      } else if (endpointMissing(e)) {
        surpriseMissing = true;
      } else {
        notice = "Couldn't pick a title.";
      }
    } finally {
      surprising = false;
    }
  }
</script>

<div class="page">
  <TopNav />
  <h1>{library?.name ?? 'Library'}</h1>

  {#if ready}
    <!-- Every chip has a focus key, so Back to the page lands on the one
         that left it (Surprise me) rather than on the first card; marked
         as controls, they don't count as loaded rows (lib/focus/memory).
         data-focus-row: Left / Right stay among the chips, so Right from
         the last one stops there instead of dropping diagonally into the
         grid (as Right from the last season chip did into the episodes). -->
    <div class="filters" data-focus-row>
      <!-- Search this library: a panel over the page (Back takes it down). -->
      <button
        use:focusable
        class="chip search"
        data-focus-key="chip:search"
        data-focus-control
        aria-label="Search this library"
        onclick={openSearch}
      >
        <svg viewBox="0 0 24 24" width="26" height="26" aria-hidden="true">
          <circle cx="10.5" cy="10.5" r="6.5" fill="none" stroke="currentColor" stroke-width="2.6" />
          <line x1="15.5" y1="15.5" x2="21" y2="21" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" />
        </svg>
        <span>Search</span>
      </button>
      <!-- An empty library with no watch chips leaves no card to land on:
           start on Sort. -->
      <button
        use:focusable={{ autofocus: firstLoad && !searchOpen && items?.length === 0 && !showWatchControls }}
        class="chip"
        data-focus-key="chip:sort"
        data-focus-control
        onclick={() => openMenu('sort')}
      >
        Sort: {sortLabel(sort)}
      </button>
      {#if showGenre}
        <button
          use:focusable
          class="chip"
          class:active={genre !== ''}
          data-focus-key="chip:genre"
          data-focus-control
          onclick={() => openMenu('genre')}
        >
          Genre: {genre || 'All genres'}
        </button>
      {/if}
      {#if showWatchControls}
        <span class="divider" aria-hidden="true"></span>
        {#each WATCH_FILTER_OPTIONS as opt (opt.value)}
          <!-- A stored filter with no matches leaves no card to land on:
               start on the active chip instead. -->
          <button
            use:focusable={{ autofocus: firstLoad && !searchOpen && items?.length === 0 && watch === opt.value }}
            class="chip"
            class:active={watch === opt.value}
            data-focus-key={`chip:watch:${opt.value}`}
            data-focus-control
            onclick={() => void setWatch(opt.value)}
          >
            {opt.label}
          </button>
        {/each}
        {#if !surpriseMissing}
          <button
            use:focusable
            class="chip surprise"
            data-focus-key="chip:surprise"
            data-focus-control
            onclick={() => void surprise()}
            disabled={surprising}
          >
            {surprising ? 'Picking…' : 'Surprise me'}
          </button>
        {/if}
      {/if}
      {#if notice}<span class="notice" role="status">{notice}</span>{/if}
    </div>
  {/if}

  <!-- The rail sits left of the grid and stays on screen as it scrolls
       (sticky). Its letters aren't keyed: Back never lands on one, and
       they don't count as loaded rows (lib/focus/memory). -->
  <div class="browse">
    {#if showRail}
      <nav class="rail" aria-label="Jump to a letter">
        {#each letters as l, li (l.letter)}
          <button
            use:focusable
            class="letter"
            class:current={li === railIndex}
            aria-label={l.letter === '#' ? 'Jump to numbers and symbols' : `Jump to ${l.letter}`}
            onclick={(e) => void jumpToLetter(l, e.currentTarget as HTMLElement)}
          >
            {l.letter}
          </button>
        {/each}
      </nav>
    {/if}

    <div class="content">
      {#if error}
        <p class="error">{error}</p>
      {:else if !items}
        <Spinner />
      {:else if items.length === 0}
        <p class="empty">{filtered ? 'Nothing matches the current filters.' : 'This library is empty.'}</p>
      {:else}
        <div class="grid">
          <!-- Opened at a letter: the first card goes in the column it has in
               the whole listing, so the rows above slot in without the grid
               shifting sideways. -->
          {#if lead > 0}<div class="lead" style="grid-column: span {lead}" aria-hidden="true"></div>{/if}
          {#each items as item, i (item.id)}
            {@const badge = cardWatchBadge(item)}
            <PosterCard
              title={item.title}
              posterPath={item.poster_path}
              subtitle={item.year ? String(item.year) : undefined}
              progressRatio={badge?.progress ?? undefined}
              watched={badge?.watched ?? false}
              unwatchedCount={badge?.unwatchedCount ?? null}
              focusKey={item.id}
              autofocus={firstLoad && !restoring && !searchOpen && i === 0}
              onclick={() => openItem(item.id, item.type)}
              onfocus={() => onCardFocus(i)}
            />
          {/each}
        </div>
        {#if loadingMore}<p class="more" role="status">Loading more…</p>{/if}
      {/if}
    </div>
  </div>
</div>

{#if loadingBefore}<p class="earlier" role="status">Loading…</p>{/if}

{#if searchOpen}
  <LibrarySearch
    libraryId={libraryID}
    libraryName={library?.name ?? 'this library'}
    query={searchText}
    memo={searchMemo}
    onquery={(q) => writeStored(searchStoreKey, q)}
    onclose={() => void closeSearch()}
  />
{/if}

{#if menu}
  <OptionsDialog
    title={menu === 'sort' ? 'Sort by' : 'Filter by genre'}
    options={menuChoice.options}
    initialIndex={menuChoice.at}
    oncancel={() => void closeMenu()}
  />
{/if}

<style>
  .page {
    padding: 0 var(--page-pad) var(--page-pad);
  }
  h1 {
    font-size: var(--font-2xl);
    margin: 24px 0 32px;
  }

  /* The chips wrap. Each child carries a top + right margin and the bar
     pulls itself up and right by the same amount, which spaces them like a
     flex `gap` would: flexbox gap needs Chrome 84, webOS 6 runs 79. */
  .filters {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    margin: -24px -16px 40px 0;
  }

  /* Combinators below put the inner part in :global(): Svelte 5 scopes
     every compound after the first as :where(.svelte-xyz), and :where()
     needs Chrome 88, so on webOS 6 (Chromium 79) the whole rule would be
     dropped. :global() leaves that part unscoped, and the rule stays valid
     (the outer class still keeps it to this component). */
  .filters > :global(*) {
    margin: 16px 16px 0 0;
  }

  .chip {
    font-family: inherit;
    font-size: var(--font-sm);
    padding: 12px 28px;
    border-radius: 999px;
    border: 2px solid var(--border);
    background: var(--bg-elevated);
    color: var(--text-primary);
    cursor: pointer;
  }

  .chip.active {
    background: var(--accent);
    border-color: var(--accent);
    color: white;
  }

  /* No combinator, so no :where; after the rule above so they win over it. */
  .divider {
    width: 2px;
    height: 40px;
    margin-left: 8px;
    margin-right: 24px;
    background: var(--border-strong);
  }

  .chip.surprise {
    margin-left: 24px;
  }

  /* The icon and its label, spaced with a margin (flexbox gap: Chrome 84). */
  .chip.search {
    display: inline-flex;
    align-items: center;
  }
  .chip.search :global(svg) {
    margin-right: 10px;
  }

  .notice {
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }

  /* The rail and the grid side by side; the rail sticks below the top
     scroll padding (110 px, app.css) as the page scrolls, so its 27 rows
     (810 px) stay on the 1080 px screen. Margins, not flexbox gap. */
  .browse {
    display: flex;
    align-items: flex-start;
  }
  .rail {
    position: sticky;
    top: 110px;
    flex: 0 0 auto;
    display: flex;
    flex-direction: column;
    margin-right: 24px;
  }
  .letter {
    width: 64px;
    height: 30px;
    padding: 0;
    border: none;
    border-radius: 8px;
    background: transparent;
    color: var(--text-secondary);
    font-family: inherit;
    font-size: var(--font-sm);
    font-weight: 700;
    line-height: 30px;
    text-align: center;
    cursor: pointer;
  }
  /* The focused card's letter. */
  .letter.current {
    color: var(--text-primary);
    background: var(--bg-elevated);
  }
  /* The attribute is the focus manager's (set at run time: :global for the
     compiler, which would call the selector unused). */
  .letter:global([data-focused='true']) {
    color: white;
    background: var(--accent);
  }
  .content {
    flex: 1 1 auto;
    min-width: 0;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: var(--card-gap) var(--card-gap);
    row-gap: calc(var(--card-gap) + 24px);
  }

  /* The page above a letter coming in: off to the side, so it doesn't move
     the grid it's being put above. */
  .earlier {
    position: fixed;
    right: var(--page-pad);
    bottom: 32px;
    margin: 0;
    padding: 8px 20px;
    border-radius: 999px;
    background: var(--bg-elevated);
    font-size: var(--font-sm);
    color: var(--text-secondary);
    z-index: 3;
  }

  .more {
    margin: 32px 0 0;
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }

  .empty {
    font-size: var(--font-md);
    color: var(--text-secondary);
  }

  .error {
    font-size: var(--font-md);
    color: #fca5a5;
  }
</style>
