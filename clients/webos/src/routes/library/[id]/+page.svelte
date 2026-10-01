<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { endpoints, Unauthorized, type MediaItem, type Library, type WatchFilter } from '$lib/api';
  import { focusable } from '$lib/focus/focusable';
  import { focusManager } from '$lib/focus/manager';
  import {
    focusFirstOf,
    restoreGuard,
    restoreKeyed,
    takeFocusMemo,
    type FocusMemo,
    type RestoreGuard,
  } from '$lib/focus/memory';
  import OptionsDialog from '$lib/components/OptionsDialog.svelte';
  import PosterCard from '$lib/components/PosterCard.svelte';
  import Spinner from '$lib/components/Spinner.svelte';
  import TopNav from '$lib/components/TopNav.svelte';
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
    (async () => {
      try {
        // The library's type picks the default sort (date-driven types open
        // newest first), so it's needed before the first page. list-by-id
        // isn't exposed; the library list is small.
        const libs = await endpoints.libraries.list();
        library = libs.find((l) => l.id === libraryID) ?? null;
        sort = storedSort ?? defaultSortFor(library?.type);
        void loadGenres();
        await loadMore();
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
        if (memo && restoring) await restoreGrid(memo, guard);
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
    return () => {
      guard.end();
      offBack();
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
      if (changed) items = pager.items;
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

  function onCardFocus(index: number) {
    if (!pager.done && nearEnd(index, items?.length ?? 0, COLUMNS)) void loadMore();
  }

  /** Start the grid over for a changed sort / genre / watch filter. With
   *  `focusFirst` the first card takes focus (Android jumps there after a
   *  sort or genre change); an empty result keeps focus on the bar. */
  async function reload(focusFirst: boolean) {
    pager.reset();
    items = null;
    error = '';
    notice = '';
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
      <!-- An empty library with no watch chips leaves no card to land on:
           start on Sort. -->
      <button
        use:focusable={{ autofocus: firstLoad && items?.length === 0 && !showWatchControls }}
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
            use:focusable={{ autofocus: firstLoad && items?.length === 0 && watch === opt.value }}
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

  {#if error}
    <p class="error">{error}</p>
  {:else if !items}
    <Spinner />
  {:else if items.length === 0}
    <p class="empty">{filtered ? 'Nothing matches the current filters.' : 'This library is empty.'}</p>
  {:else}
    <div class="grid">
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
          autofocus={firstLoad && !restoring && i === 0}
          onclick={() => openItem(item.id, item.type)}
          onfocus={() => onCardFocus(i)}
        />
      {/each}
    </div>
    {#if loadingMore}<p class="more" role="status">Loading more…</p>{/if}
  {/if}
</div>

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

  .notice {
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: var(--card-gap) var(--card-gap);
    row-gap: calc(var(--card-gap) + 24px);
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
