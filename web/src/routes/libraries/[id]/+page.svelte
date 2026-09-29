<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto, replaceState } from '$app/navigation';
  import { page } from '$app/stores';
  import { libraryApi, mediaApi, itemApi, assetUrl, type Library, type MediaItem, type SortField, type ListItemsParams, type GenreCount, type EventCollection, type WatchFilter } from '$lib/api';
  import { itemHref as resolveItemHref } from '$lib/itemHref';
  import PlaylistPicker from '$lib/components/PlaylistPicker.svelte';
  import MetadataEditor from '$lib/components/MetadataEditor.svelte';
  import WatchBadge from '$lib/components/WatchBadge.svelte';
  import CardMenu from '$lib/components/CardMenu.svelte';
  import CollectionsTab from '$lib/components/CollectionsTab.svelte';
  import AlbumPicker from '$lib/components/AlbumPicker.svelte';
  import { toast } from '$lib/stores/toast';
  import { toggleSelected } from '$lib/photoAlbums';
  import {
    WATCH_FILTER_OPTIONS,
    parseWatchFilter,
    urlWithWatchFilter,
    supportsWatchState,
    canMarkWatched,
    watchMenuActions,
    applyWatchedMark,
    isNotFound,
  } from '$lib/watchState';
  import {
    MUSIC_SORTS,
    defaultSortAsc,
    sortOffered,
    musicViewFromURL,
    musicBrowseURL,
    type MusicView,
  } from '$lib/musicBrowse';

  let playlistPickerItemId = '';
  let showPlaylistPicker = false;

  // Photo libraries: Select mode, where a tile click toggles the photo in
  // or out of the selection instead of opening it, and the selection goes
  // to an album through the AlbumPicker.
  let selectingPhotos = false;
  let selectedPhotoIds = new Set<string>();
  let albumPickerOpen = false;

  function toggleSelectPhotos() {
    selectingPhotos = !selectingPhotos;
    selectedPhotoIds = new Set();
  }

  function onGridItemClick(e: MouseEvent, item: MediaItem) {
    if (!selectingPhotos || !isPhotoLibrary) return;
    e.preventDefault();
    selectedPhotoIds = toggleSelected(selectedPhotoIds, item.id);
  }

  // Per-tile metadata editor — admin-only, triggered by the ✎ overlay
  // button on home_video tiles (no external metadata source so the
  // user owns title/summary/taken_at). Bound to a single shared modal
  // instance so we don't spawn one per tile.
  let isAdmin = false;
  let editingItem: MediaItem | null = null;
  let editMetadataOpen = false;

  function openMetadataEditor(e: MouseEvent, item: MediaItem) {
    e.preventDefault();
    e.stopPropagation();
    editingItem = item;
    editMetadataOpen = true;
  }

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
  // musicVideos sits alongside the artist grid on a music library
  // page — videos hang off artists in the schema (no album), so the
  // library landing page renders them as a separate shelf above the
  // artists. Empty for any non-music library.
  let musicVideos: MediaItem[] = [];
  // eventCollections renders an "Events" shelf on home_video libraries:
  // one tile per non-root subfolder under the library root, auto-
  // created by the scanner. Empty for any other library type.
  let eventCollections: EventCollection[] = [];
  let loadingLib = true;
  let loadingItems = false;
  let scanning = false;
  let error = '';
  let enrichTimeout = '';

  const PAGE = 48;
  const BATCH = 24;
  let offset = 0;
  let total = 0;
  let hasMore = false;

  let query = '';
  let sortField: SortField = 'title';
  let sortAsc = true;
  let sortDefaulted = false;

  // Filters
  let genres: GenreCount[] = [];
  let selectedGenre = '';
  let yearMin = '';
  let yearMax = '';
  let ratingMin = '';
  // Caller's watch state (?watch=unwatched|in_progress|watched); '' = all.
  let watchFilter: WatchFilter | '' = '';

  // Music libraries browse either their artists (the top-level rows) or
  // every album in the library (?view=albums). See $lib/musicBrowse.
  let musicView: MusicView = 'artists';

  // Hydrate filters from URL on first load (?genre=Drama, ?year_min=, ?year_max=)
  // so deep-links from the genre/year browse pages preselect the correct filter.
  // Also reads ?sort= and ?sort_dir= so the home page's "Recently Added"
  // shelf can land here on /libraries/{id}?sort=created_at&sort_dir=desc and
  // have the right pill pre-selected — no extra click to flip the sort.
  // Also re-run when the URL changes under a mounted page (another library,
  // or Back/Forward between two music views), so it resets what the URL
  // leaves out.
  function readFiltersFromURL() {
    const sp = $page.url.searchParams;
    selectedGenre = sp.get('genre') ?? '';
    yearMin = sp.get('year_min') ?? '';
    yearMax = sp.get('year_max') ?? '';
    watchFilter = parseWatchFilter(sp.get('watch'));
    libTab = tabFromURL($page.url);
    musicView = musicViewFromURL($page.url);

    const s = sp.get('sort');
    if (s === 'title' || s === 'year' || s === 'rating' || s === 'created_at' || s === 'taken_at' || s === 'artist') {
      sortField = s;
      // Preserve sort_dir from the URL when present; otherwise pick a
      // sensible default per field (newest-first for time-based sorts,
      // ascending for names).
      const dir = sp.get('sort_dir');
      if (dir === 'asc') sortAsc = true;
      else if (dir === 'desc') sortAsc = false;
      else sortAsc = defaultSortAsc(s);
      // Mark as user-driven so loadLibrary's photo/home-video default
      // doesn't stomp on the URL-supplied sort.
      sortDefaulted = true;
    } else {
      sortField = 'title';
      sortAsc = true;
      sortDefaulted = false;
    }
  }

  // The query string the grid currently reflects. A navigation that changes
  // $page.url without remounting the page (Back/Forward between two music
  // views, or a link to this same library) must be followed; our own
  // writeMusicURL navigations are recorded here first so they aren't.
  let appliedSearch = '';

  // id and prevId are passed so this runs after `id` is derived and after
  // the id block below: a different library is that block's job.
  $: if (mounted) followURL($page.url, id, prevId);

  function followURL(url: URL, currentId: string, loadedId: string) {
    if (currentId !== loadedId || url.search === appliedSearch) return;
    appliedSearch = url.search;
    const viewBefore = musicView;
    readFiltersFromURL();
    applyLibraryDefaultSort();
    if (musicView !== viewBefore) {
      genres = [];
      loadGenres();
    }
    applyFilters();
  }

  // Music libraries keep their browse state (view, sort, genre, years) in
  // the URL through a real navigation rather than a shallow replaceState:
  // SvelteKit hands a shallow entry its original $page.url back on Back,
  // which would lose the view on the way back from an album. Switching
  // views adds a history entry; sort and filter changes replace the
  // current one.
  function writeMusicURL(push = false) {
    if (library?.type !== 'music') return;
    const url = musicBrowseURL($page.url, {
      view: musicView,
      sort: sortField,
      sortAsc,
      genre: selectedGenre,
      yearMin,
      yearMax,
    });
    appliedSearch = url.search;
    goto(url, { replaceState: !push, noScroll: true, keepFocus: true });
  }

  $: albumsView = isMusicLibrary && musicView === 'albums';

  // Artists ↔ Albums. Genres and years exist only on albums, so the facets
  // are reloaded and the genre / year filters cleared; the sort is kept when
  // the other view offers it.
  function setMusicView(next: MusicView) {
    if (next === musicView) return;
    musicView = next;
    selectedGenre = '';
    yearMin = '';
    yearMax = '';
    if (!sortOffered(next, sortField)) {
      sortField = 'title';
      sortAsc = true;
    }
    writeMusicURL(true);
    genres = [];
    loadGenres();
    applyFilters();
  }

  function clearYears() {
    yearMin = '';
    yearMax = '';
    writeMusicURL();
    applyFilters();
  }

  function clearFilters() {
    selectedGenre = '';
    clearYears();
  }

  let mounted = false;
  let prevId = '';

  $: id = $page.params.id!;

  $: isPhotoLibrary = library?.type === 'photo';
  $: isMusicLibrary = library?.type === 'music';
  // Audiobook libraries surface authors at the top level (the typed
  // shelf — same shape as music's artist row). Round-poster + label-
  // overlay-suppressed treatment matches the artist cards visually.
  $: isAudiobookLibrary = library?.type === 'audiobook';
  $: isHomeVideoLibrary = library?.type === 'home_video';

  // Movie libraries get a "Collections" tab: TMDB film series with films in
  // this library (CollectionsTab). ?tab=collections keeps the choice across
  // a reload and Back from a collection page.
  let libTab: 'items' | 'collections' = 'items';
  $: hasCollectionsTab = library?.type === 'movie';
  function tabFromURL(u: URL): 'items' | 'collections' {
    return u.searchParams.get('tab') === 'collections' ? 'collections' : 'items';
  }
  function setLibTab(next: 'items' | 'collections') {
    libTab = next;
    try {
      const u = new URL($page.url);
      if (next === 'collections') u.searchParams.set('tab', 'collections');
      else u.searchParams.delete('tab');
      replaceState(u, $page.state);
    } catch { /* router not ready (tests / early mount) — the tab still switches */ }
  }

  // Date-grouped buckets for home-video libraries: [{ key, label, items }].
  // Group key is "YYYY-MM" so chronological sort just works on the key;
  // label is the human-friendly "April 2024" form for the section header.
  // Items missing taken_at land in an "Undated" bucket so they're still
  // visible (no silent dropping).
  $: dateBuckets = (() => {
    if (!isHomeVideoLibrary) return [] as Array<{ key: string; label: string; items: MediaItem[] }>;
    const map = new Map<string, MediaItem[]>();
    for (const it of filtered) {
      let key = '0000-00';
      if (it.taken_at) {
        const d = new Date(it.taken_at);
        if (!isNaN(d.getTime())) {
          key = `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, '0')}`;
        }
      }
      const arr = map.get(key) ?? [];
      arr.push(it);
      map.set(key, arr);
    }
    const fmt = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric', timeZone: 'UTC' });
    return [...map.entries()]
      .sort((a, b) => b[0].localeCompare(a[0])) // newest first
      .map(([key, items]) => {
        let label = 'Undated';
        if (key !== '0000-00') {
          const [y, m] = key.split('-').map(Number);
          label = fmt.format(new Date(Date.UTC(y, m - 1, 1)));
        }
        return { key, label, items };
      });
  })();

  // Top-level items in a music library are artists; in show libraries, shows;
  // in photo libraries, photos; in podcast libraries, the podcast show. Each
  // routes to a different detail view; everything else falls back to the
  // watch page (which itself bounces movie/episode-shaped types).
  function itemHref(item: MediaItem): string {
    return resolveItemHref(item.type, item.id);
  }

  // Client-side text filter on already-loaded items. Albums also match on
  // their artist.
  $: filtered = query
    ? allItems.filter(i => i.title.toLowerCase().includes(query.toLowerCase())
        || !!i.parent_title?.toLowerCase().includes(query.toLowerCase()))
    : allItems;

  const PHOTO_SORTS: ReadonlyArray<readonly [SortField, string]> =
    [['taken_at', 'Taken'], ['created_at', 'Added'], ['title', 'Title']];
  $: sortOptions = isPhotoLibrary ? PHOTO_SORTS : MUSIC_SORTS[albumsView ? 'albums' : 'artists'];

  function filterParams(): ListItemsParams {
    const p: ListItemsParams = { sort: sortField, sort_dir: sortAsc ? 'asc' : 'desc' };
    if (library?.type === 'music' && musicView === 'albums') p.type = 'album';
    if (selectedGenre) p.genre = selectedGenre;
    if (yearMin) p.year_min = parseInt(yearMin);
    if (yearMax) p.year_max = parseInt(yearMax);
    if (ratingMin) p.rating_min = parseFloat(ratingMin);
    if (watchFilter && supportsWatchState(library?.type)) p.watch = watchFilter;
    return p;
  }

  // ── Watch state: filter, Surprise me, mark watched ──────────────────────
  $: watchable = supportsWatchState(library?.type);

  // The filter lives in ?watch= (same as genre/year deep-links), so a
  // reload or Back from an item keeps it.
  function setWatchFilter(next: WatchFilter | '') {
    watchFilter = next;
    try {
      replaceState(urlWithWatchFilter($page.url, next), $page.state);
    } catch { /* router not ready (tests / early mount) — the filter still applies */ }
    surpriseMsg = '';
    applyFilters();
  }

  let surprising = false;
  let surpriseMsg = '';

  async function surpriseMe() {
    if (surprising) return;
    surprising = true;
    surpriseMsg = '';
    try {
      // Same filters as the grid; randomItem ignores the sort fields.
      const pick = await mediaApi.randomItem(id, filterParams());
      goto(resolveItemHref(pick.type, pick.id));
    } catch (e: unknown) {
      surpriseMsg = isNotFound(e)
        ? 'Nothing matches these filters — try widening them.'
        : (e instanceof Error ? e.message : 'Could not pick something right now.');
    } finally {
      surprising = false;
    }
  }

  let markingIds = new Set<string>();

  // Optimistic: flip the badge now, roll back if the server says no.
  async function markItem(item: MediaItem, watched: boolean) {
    if (markingIds.has(item.id)) return;
    markingIds = new Set(markingIds).add(item.id);
    const before = item;
    const replace = (next: MediaItem) => {
      allItems = allItems.map((x) => (x.id === before.id ? next : x));
    };
    replace(applyWatchedMark(before, watched));
    try {
      if (watched) await itemApi.markWatched(before.id);
      else await itemApi.markUnwatched(before.id);
    } catch (e: unknown) {
      replace(before);
      toast.error(e instanceof Error ? e.message : `Could not mark "${before.title}" ${watched ? 'watched' : 'unwatched'}`);
    } finally {
      const next = new Set(markingIds);
      next.delete(before.id);
      markingIds = next;
    }
  }

  function cardActions(item: MediaItem) {
    return watchMenuActions(item).map((a) => ({
      label: a === 'watched' ? 'Mark watched' : 'Mark unwatched',
      onSelect: () => markItem(item, a === 'watched'),
    }));
  }

  function infiniteScroll(node: HTMLElement) {
    let pending = false;
    // Use the actual scroll container (.main) as root so rootMargin works correctly.
    // With root: null the viewport is used, but .main is the real scroll ancestor
    // (the shell has overflow: hidden), so the 600px margin never fires early.
    const scrollRoot = node.closest('main') as HTMLElement | null;
    const obs = new IntersectionObserver(async (entries) => {
      if (entries[0]?.isIntersecting && hasMore && !loadingItems && !pending) {
        pending = true;
        await loadItems(true);
        pending = false;
        // Re-trigger: if sentinel is still visible after new items pushed it down,
        // unobserve/re-observe so the observer re-evaluates
        if (hasMore) {
          obs.unobserve(node);
          obs.observe(node);
        }
      }
    }, { root: scrollRoot, rootMargin: '600px' });
    obs.observe(node);
    return { destroy() { obs.disconnect(); } };
  }

  onMount(async () => {
    const raw = localStorage.getItem('onscreen_user');
    if (!raw) { goto('/login'); return; }
    try { isAdmin = !!JSON.parse(raw)?.is_admin; } catch { /* keep false */ }
    prevId = id;
    appliedSearch = $page.url.search;
    readFiltersFromURL();
    // Await library first so we can pick the right default sort before listing.
    await loadLibrary();
    await Promise.all([loadItems(), loadGenres(), loadMusicVideos(), loadEventCollections()]);
    mounted = true;
  });

  onDestroy(() => { alive = false; });

  $: if (mounted && id && id !== prevId) {
    prevId = id;
    appliedSearch = $page.url.search;
    allItems = [];
    musicVideos = [];
    eventCollections = [];
    offset = 0;
    total = 0;
    hasMore = true;
    loadingLib = true;
    loadingItems = true;
    error = '';
    library = null;
    genres = [];
    // Filters, sort, watch filter, tab and music view all come from the
    // new library's URL — nothing carries over from the previous one.
    readFiltersFromURL();
    surpriseMsg = '';
    selectingPhotos = false;
    selectedPhotoIds = new Set();
    loadLibrary().then(() => {
      loadItems();
      loadGenres();
      loadMusicVideos();
      loadEventCollections();
    });
  }

  async function loadLibrary() {
    try {
      library = await libraryApi.get(id);
      applyLibraryDefaultSort();
    }
    catch (e: unknown) { error = e instanceof Error ? e.message : 'Failed'; }
    finally { loadingLib = false; }
  }

  // Photo + home-video libraries sort by date taken by default —
  // alphabetic title is hostile when items are date-stamped events
  // ("2024-04-15 - Hike" wouldn't sort by recency without this).
  // User can still override via the sort menu.
  function applyLibraryDefaultSort() {
    if ((library?.type === 'photo' || library?.type === 'home_video') && !sortDefaulted) {
      sortField = 'taken_at';
      sortAsc = false;
      sortDefaulted = true;
    }
  }

  // A music library's albums view filters by the albums' own genres; the
  // artists it lists otherwise carry none.
  async function loadGenres() {
    const capturedId = id;
    const capturedView = musicView;
    const type = library?.type === 'music' && musicView === 'albums' ? 'album' : undefined;
    try {
      const g = await mediaApi.genres(capturedId, type);
      if (id === capturedId && musicView === capturedView) genres = g;
    }
    catch { /* non-critical */ }
  }

  // Home-video libraries get a separate "Events" shelf above the
  // date-bucketed grid. Skipped on every other library type. The
  // /event-collections endpoint returns [] for any non-home-video
  // library, but skipping the call avoids a useless round-trip.
  async function loadEventCollections() {
    if (library?.type !== 'home_video') {
      eventCollections = [];
      return;
    }
    try {
      eventCollections = await mediaApi.eventCollections(id);
    } catch { /* non-critical — shelf just won't render */ }
  }

  // Music libraries get a separate Music Videos shelf above the artist
  // grid. Skipped on every other library type — the type-override is
  // server-validated, so a podcast library would 400 on this call.
  // Capped at a small batch — if the user has more than 24 music videos
  // we surface a "View all" affordance and keep the shelf compact.
  async function loadMusicVideos() {
    if (library?.type !== 'music') {
      musicVideos = [];
      return;
    }
    try {
      const r = await mediaApi.listItems(id, 24, 0, { type: 'music_video', sort: 'title', sort_dir: 'asc' });
      musicVideos = r.items;
    } catch { /* non-critical — shelf just won't render */ }
  }

  // Each listing request is numbered; a response that a newer request
  // superseded (a view, sort or filter change mid-flight) is dropped, so
  // artists never land in the albums grid or vice versa.
  let listSeq = 0;

  async function loadItems(append = false) {
    const seq = ++listSeq;
    loadingItems = true;
    try {
      const limit = append ? BATCH : PAGE;
      const r = await mediaApi.listItems(id, limit, append ? offset : 0, filterParams());
      if (seq !== listSeq) return;
      allItems = append ? [...allItems, ...r.items] : r.items;
      total = r.total;
      offset = append ? offset + r.items.length : r.items.length;
      hasMore = offset < total;
    } catch (e: unknown) { if (seq === listSeq) error = e instanceof Error ? e.message : 'Failed'; }
    finally { if (seq === listSeq) loadingItems = false; }
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
    else { sortField = f; sortAsc = defaultSortAsc(f); }
    writeMusicURL();
    applyFilters();
  }

  // Read from the event rather than a bind:value, so the listing never
  // races the binding for which one sees the change first.
  function setGenre(next: string) {
    selectedGenre = next;
    writeMusicURL();
    applyFilters();
  }

  function dur(ms?: number) {
    if (!ms) return '';
    const m = Math.round(ms / 60000);
    return m < 60 ? `${m}m` : `${Math.floor(m / 60)}h ${m % 60}m`;
  }

  const typeColor: Record<string, string> = {
    movie: '#60a5fa', show: '#a78bfa', music: '#34d399', photo: '#fb923c',
    audiobook: '#f59e0b', podcast: '#ec4899',
    home_video: '#10b981', book: '#8b5cf6', dvr: '#94a3b8',
  };
  const typeLabel: Record<string, string> = {
    movie: 'Movies', show: 'TV Shows', music: 'Music', photo: 'Photos',
    audiobook: 'Audiobooks', podcast: 'Podcasts',
    home_video: 'Home Videos', book: 'Books', dvr: 'DVR Recordings',
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

  {#if hasCollectionsTab}
    <div class="lib-tabs" role="tablist" aria-label="Library view">
      <button type="button" role="tab" aria-selected={libTab === 'items'} class:on={libTab === 'items'}
              on:click={() => setLibTab('items')}>Library</button>
      <button type="button" role="tab" aria-selected={libTab === 'collections'} class:on={libTab === 'collections'}
              on:click={() => setLibTab('collections')}>Collections</button>
    </div>
  {/if}

  {#if isMusicLibrary}
    <div class="lib-tabs" role="tablist" aria-label="Browse music by">
      <button type="button" role="tab" aria-selected={musicView === 'artists'} class:on={musicView === 'artists'}
              on:click={() => setMusicView('artists')}>Artists</button>
      <button type="button" role="tab" aria-selected={musicView === 'albums'} class:on={musicView === 'albums'}
              on:click={() => setMusicView('albums')}>Albums</button>
    </div>
  {/if}

  {#if hasCollectionsTab && libTab === 'collections'}
    <CollectionsTab libraryId={id} />
  {:else}
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
      {#each sortOptions as [f, l]}
        <button class="sort-pill" class:on={sortField === f} on:click={() => toggleSort(f)}>
          {l}{sortField === f ? (sortAsc ? ' ↑' : ' ↓') : ''}
        </button>
      {/each}
    </div>

    {#if genres.length > 0}
      <select class="filter-select" aria-label="Filter by genre" value={selectedGenre}
              on:change={(e) => setGenre(e.currentTarget.value)}>
        <option value="">All Genres</option>
        {#each genres as g}
          <option value={g.name}>{g.name} ({g.count})</option>
        {/each}
      </select>
    {/if}

    {#if yearMin || yearMax}
      <!-- A year filter arrives from Browse years; show it so it can be cleared. -->
      <span class="filter-chip">
        {yearMin === yearMax ? yearMin : `${yearMin || '…'}–${yearMax || '…'}`}
        <button type="button" aria-label="Clear year filter" on:click={clearYears}>×</button>
      </span>
    {/if}

    {#if watchable}
      <select
        class="filter-select"
        aria-label="Filter by watch state"
        value={watchFilter}
        on:change={(e) => setWatchFilter(parseWatchFilter(e.currentTarget.value))}
      >
        {#each WATCH_FILTER_OPTIONS as opt (opt.value)}
          <option value={opt.value}>{opt.label}</option>
        {/each}
      </select>
      <button
        type="button"
        class="surprise-btn"
        disabled={surprising}
        on:click={surpriseMe}
        title="Open a random item that matches the current filters"
      >
        <svg viewBox="0 0 16 16" fill="currentColor" width="13" height="13" aria-hidden="true">
          <path d="M2.5 1A1.5 1.5 0 001 2.5v11A1.5 1.5 0 002.5 15h11a1.5 1.5 0 001.5-1.5v-11A1.5 1.5 0 0013.5 1h-11zM5 4a1 1 0 110 2 1 1 0 010-2zm6 6a1 1 0 110 2 1 1 0 010-2zm-3-3a1 1 0 110 2 1 1 0 010-2zm3-3a1 1 0 110 2 1 1 0 010-2zM5 10a1 1 0 110 2 1 1 0 010-2z"/>
        </svg>
        {surprising ? 'Picking…' : 'Surprise me'}
      </button>
    {/if}

    {#if isPhotoLibrary}
      <button
        type="button"
        class="surprise-btn"
        class:on={selectingPhotos}
        aria-pressed={selectingPhotos}
        on:click={toggleSelectPhotos}
        title="Select photos to add to an album"
      >{selectingPhotos ? 'Done' : 'Select'}</button>
    {/if}

    <div class="browse-links">
      {#if isPhotoLibrary}
        <a href="/photos/map?library={id}">Map</a>
        <a href="/photos/albums">Albums</a>
      {/if}
      <a href="/libraries/{id}/genres">Browse genres</a>
      <a href="/libraries/{id}/years">Browse years</a>
    </div>

    <div class="count">
      {#if query}{filtered.length} / {allItems.length}{:else if albumsView}{total} {total === 1 ? 'album' : 'albums'}{:else}{total} items{/if}
    </div>
  </div>
  <p class="surprise-msg" role="status" aria-live="polite">{surpriseMsg}</p>

  <!-- Grid -->
  {#if allItems.length === 0 && !loadingItems && watchable && watchFilter}
    <div class="empty">
      <p class="empty-t">Nothing {WATCH_FILTER_OPTIONS.find((o) => o.value === watchFilter)?.label.toLowerCase()} here</p>
      <button class="clear-link" on:click={() => setWatchFilter('')}>Show all items</button>
    </div>
  {:else if allItems.length === 0 && !loadingItems && albumsView && (selectedGenre || yearMin || yearMax)}
    <div class="empty">
      <p class="empty-t">No albums match these filters</p>
      <button class="clear-link" on:click={clearFilters}>Show all albums</button>
    </div>
  {:else if allItems.length === 0 && !loadingItems}
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
    {#if isMusicLibrary && !albumsView && musicVideos.length > 0}
      <section class="mv-shelf">
        <h2 class="mv-shelf-title">Music videos</h2>
        <div class="mv-row">
          {#each musicVideos as v (v.id)}
            <a class="mv-card" href="/watch/{v.id}" title={v.title}>
              <div class="mv-thumb">
                {#if v.poster_path}
                  <img src={assetUrl(`/artwork/${encodeURI(v.poster_path)}?v=${v.updated_at}&w=320`)}
                       alt={v.title} loading="lazy" />
                {:else}
                  <div class="mv-thumb-blank">{v.title[0]?.toUpperCase()}</div>
                {/if}
                <div class="mv-play">▶</div>
              </div>
              <div class="mv-meta">
                <div class="mv-title">{v.title}</div>
                {#if v.duration_ms}<div class="mv-dur">{dur(v.duration_ms)}</div>{/if}
              </div>
            </a>
          {/each}
        </div>
      </section>
    {/if}
    {#if isHomeVideoLibrary && eventCollections.length > 0}
      <!-- Events shelf: one tile per non-root subfolder under the
           library root (e.g. "Yellowstone 2024"). Auto-created by
           the scanner; clicking lands on the collection detail page
           which renders the bundled clips. Skipped when the library
           is fully flat (every file at root). -->
      <section class="events-shelf">
        <h2 class="events-title">Events</h2>
        <div class="events-row">
          {#each eventCollections as ev (ev.id)}
            <a class="event-card" href="/collections/{ev.id}" title={ev.name}>
              <div class="event-thumb">
                {#if ev.poster_path}
                  <img src={assetUrl(`/artwork/${encodeURI(ev.poster_path)}?w=320`)}
                       alt={ev.name} loading="lazy" />
                {:else}
                  <div class="event-thumb-blank">{ev.name[0]?.toUpperCase() ?? '◇'}</div>
                {/if}
              </div>
              <div class="event-name">{ev.name}</div>
            </a>
          {/each}
        </div>
      </section>
    {/if}
    {#if isHomeVideoLibrary}
      <!-- Home-video libraries render as a chronologically-bucketed
           grid (one section per month) instead of one big alphabetic
           grid. Items within a bucket keep the user-selected sort. -->
      {#each dateBuckets as bucket (bucket.key)}
        <h2 class="bucket-title">{bucket.label}</h2>
        <div class="grid bucket-grid">
          {#each bucket.items as item (item.id)}
            <a class="item" href={itemHref(item)} tabindex="0">
              <div class="poster">
                {#if item.poster_path}
                  <img src={assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=300`)}
                       srcset="{assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=150`)} 150w, {assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=300`)} 300w"
                       sizes="(max-width: 768px) 100px, 180px"
                       alt={item.title} loading="lazy" />
                {:else}
                  <div class="poster-blank"><span>▶</span></div>
                {/if}
                <div class="poster-overlay">
                  <div class="play-icon">▶</div>
                  <div class="overlay-title">{item.title}</div>
                  <div class="overlay-meta">
                    {#if item.taken_at}{new Date(item.taken_at).toLocaleDateString()}{/if}
                    {#if item.duration_ms} · {dur(item.duration_ms)}{/if}
                  </div>
                </div>
                {#if isAdmin}
                  <button class="edit-meta-btn" title="Edit title, summary, date"
                          aria-label="Edit metadata"
                          on:click={(e) => openMetadataEditor(e, item)}>✎</button>
                {/if}
                <WatchBadge {item} />
              </div>
            </a>
          {/each}
        </div>
      {/each}
    {:else if albumsView}
      <!-- Albums index: every album in the library as a square cover with
           its title, then its artist and year. The artist is a link of its
           own, beside the card link rather than nested in it. -->
      <div class="grid album-grid">
        {#each filtered as item (item.id)}
          <div class="item-cell">
            <a class="item" href={itemHref(item)}>
              <div class="poster">
                {#if item.poster_path}
                  <img src={assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=300`)}
                       srcset="{assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=150`)} 150w, {assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=300`)} 300w, {assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=450`)} 450w"
                       sizes="(max-width: 768px) 100px, 180px"
                       alt={item.title} loading="lazy" />
                {:else}
                  <div class="poster-blank"><span>♪</span></div>
                  <button
                    class="refresh-art"
                    class:spinning={enrichingIds.has(item.id)}
                    title="Refresh artwork"
                    on:click={(e) => enrichItem(e, item.id)}
                  >⟳</button>
                {/if}
              </div>
              <div class="item-foot">
                <div class="item-title">{item.title}</div>
              </div>
            </a>
            <div class="album-meta">
              {#if item.parent_title && item.parent_id}
                <a class="album-artist" href="/artists/{item.parent_id}">{item.parent_title}</a>
              {:else if item.parent_title}
                <span class="album-artist">{item.parent_title}</span>
              {/if}
              {#if item.parent_title && item.year}<span aria-hidden="true">·</span>{/if}
              {#if item.year}<span class="album-year">{item.year}</span>{/if}
            </div>
          </div>
        {/each}

        {#if loadingItems}
          {#each {length: 8} as _}
            <div class="item skeleton-item">
              <div class="poster skeleton-poster"></div>
            </div>
          {/each}
        {/if}
      </div>
    {:else}
    <div class="grid" class:photo-grid={isPhotoLibrary} class:music-grid={isMusicLibrary || isAudiobookLibrary}>
      {#each filtered as item (item.id)}
        {@const withMenu = watchable && canMarkWatched(item.type)}
        <!-- The cell holds the card link plus the ⋯ menu as a sibling (not
             nested in the link), so the menu is its own tab stop. -->
        {@const photoSelected = selectingPhotos && selectedPhotoIds.has(item.id)}
        <div class="item-cell" class:has-menu={withMenu}>
        <a
          class="item"
          class:circle-poster={isMusicLibrary || (isAudiobookLibrary && item.type === 'book_author')}
          class:photo-selected={photoSelected}
          href={itemHref(item)}
          tabindex="0"
          aria-label={selectingPhotos && isPhotoLibrary ? `${photoSelected ? 'Deselect' : 'Select'} ${item.title}` : undefined}
          on:click={(e) => onGridItemClick(e, item)}
        >
          <div class="poster">
            {#if item.poster_path}
              <img src={assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=300`)}
                   srcset="{assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=150`)} 150w, {assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=300`)} 300w, {assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}&w=450`)} 450w"
                   sizes="(max-width: 768px) 100px, 180px"
                   alt={item.title} loading="lazy" />
            {:else}
              <div class="poster-blank">
                <span>{item.title[0]?.toUpperCase()}</span>
              </div>
            {/if}
            <div class="poster-overlay">
              {#if !isPhotoLibrary && !isMusicLibrary && !isAudiobookLibrary}<div class="play-icon">▶</div>{/if}
              <div class="overlay-title">{item.title}</div>
              {#if item.type === 'audiobook' && item.original_title}
                <div class="overlay-meta">by {item.original_title}{#if item.duration_ms} · {dur(item.duration_ms)}{/if}</div>
              {:else if item.type === 'book_author' || item.type === 'book_series'}
                <!-- Author + series cards lean on the title alone; child
                     count would need a separate /children fetch and
                     isn't worth the request count on a grid render. -->
              {:else if !isPhotoLibrary && !isMusicLibrary}
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
            {#if !isMusicLibrary && !isPhotoLibrary && !isAudiobookLibrary}
              <button
                class="add-playlist-btn"
                title="Add to playlist"
                on:click={(e) => openPlaylistPicker(e, item.id)}
              >+</button>
            {/if}
            {#if selectingPhotos && isPhotoLibrary}
              <span class="select-check" aria-hidden="true">{photoSelected ? '✓' : ''}</span>
            {/if}
            {#if isAdmin && isPhotoLibrary && !selectingPhotos}
              <button
                class="edit-meta-btn"
                title="Edit title, summary, date"
                aria-label="Edit metadata"
                on:click={(e) => openMetadataEditor(e, item)}
              >✎</button>
            {/if}
            <WatchBadge {item} />
          </div>
          <div class="item-foot">
            <div class="item-title">{item.title}</div>
            {#if item.year}<div class="item-year">{item.year}</div>{/if}
          </div>
        </a>
        {#if withMenu}
          <div class="item-menu">
            <CardMenu label="More actions for {item.title}" actions={cardActions(item)} />
          </div>
        {/if}
        </div>
      {/each}

      {#if loadingItems}
        {#each {length: 8} as _}
          <div class="item skeleton-item">
            <div class="poster skeleton-poster"></div>
          </div>
        {/each}
      {/if}
    </div>
    {/if}

    {#if hasMore}
      <div class="scroll-sentinel" use:infiniteScroll></div>
      {#if loadingItems}
        <div class="loading-more">
          <span class="spin">&#8635;</span> Loading…
        </div>
      {/if}
    {/if}
  {/if}
  {/if}
</div>

<PlaylistPicker
  mediaItemId={playlistPickerItemId}
  open={showPlaylistPicker}
  on:close={() => showPlaylistPicker = false}
/>

{#if selectingPhotos && isPhotoLibrary}
  <div class="select-bar" role="toolbar" aria-label="Selected photos">
    <span class="sel-count" role="status">{selectedPhotoIds.size} selected</span>
    <button
      type="button"
      class="btn-scan"
      disabled={selectedPhotoIds.size === 0}
      on:click={() => albumPickerOpen = true}
    >Add to album…</button>
    <button type="button" class="btn-refresh sel-done" on:click={toggleSelectPhotos}>Done</button>
  </div>
{/if}

{#if isPhotoLibrary}
  <AlbumPicker
    open={albumPickerOpen}
    mediaItemIds={[...selectedPhotoIds]}
    onclose={() => albumPickerOpen = false}
    ondone={() => { selectingPhotos = false; selectedPhotoIds = new Set(); }}
  />
{/if}

{#if editingItem}
  <MetadataEditor
    itemId={editingItem.id}
    initialTitle={editingItem.title}
    initialSummary={editingItem.summary}
    initialTakenAt={editingItem.taken_at}
    open={editMetadataOpen}
    on:close={() => { editMetadataOpen = false; editingItem = null; }}
    on:saved={(e) => {
      // Reflect the edit in the local grid without re-fetching the
      // whole list. Date changes can re-bucket the item; the
      // dateBuckets reactive picks that up automatically when
      // allItems changes.
      if (editingItem) {
        const id = editingItem.id;
        allItems = allItems.map(x => x.id === id
          ? { ...x, title: e.detail.title, summary: e.detail.summary ?? undefined, taken_at: e.detail.taken_at ?? undefined }
          : x);
      }
    }}
  />
{/if}

<style>
  .page { padding: 2.5rem 2.5rem 5rem; }

  .lib-tabs { display: flex; gap: 0.35rem; margin: 0 0 1.25rem; border-bottom: 1px solid var(--border); }
  .lib-tabs button {
    background: none; border: none; border-bottom: 2px solid transparent; margin-bottom: -1px;
    padding: 0.45rem 0.8rem; font-size: 0.82rem; font-weight: 600; color: var(--text-muted); cursor: pointer;
  }
  .lib-tabs button:hover { color: var(--text-secondary); }
  .lib-tabs button.on { color: var(--text-primary); border-bottom-color: var(--accent); }

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

  .surprise-btn {
    display: inline-flex; align-items: center; gap: 0.35rem;
    padding: 0.35rem 0.7rem;
    background: var(--bg-hover);
    border: 1px solid var(--border-strong);
    border-radius: 7px;
    font-size: 0.75rem;
    color: var(--text-secondary);
    cursor: pointer;
    white-space: nowrap;
    transition: border-color 0.12s, color 0.12s;
  }
  .surprise-btn:hover:not(:disabled) { border-color: var(--accent); color: var(--text-primary); }
  .surprise-btn:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
  .surprise-btn:disabled { opacity: 0.6; cursor: progress; }
  .surprise-msg { margin: -1rem 0 1.25rem; font-size: 0.78rem; color: var(--text-muted); }
  .surprise-msg:empty { margin: 0; }
  /* Photo Select toggle, when on. */
  .surprise-btn.on { background: var(--accent-bg); border-color: rgba(124,106,247,0.3); color: var(--accent-text); }

  /* Photo Select mode: a check on each tile and a floating action bar. */
  .item.photo-selected .poster { box-shadow: 0 0 0 3px var(--accent); }
  .item.photo-selected .poster img { opacity: 0.8; }
  .select-check {
    position: absolute; top: 0.4rem; left: 0.4rem; z-index: 2;
    width: 22px; height: 22px; border-radius: 50%;
    display: flex; align-items: center; justify-content: center;
    border: 2px solid #fff; background: rgba(0,0,0,0.35);
    color: #fff; font-size: 0.75rem; font-weight: 800;
  }
  .item.photo-selected .select-check { background: var(--accent); border-color: var(--accent); }
  .select-bar {
    position: fixed; left: 50%; bottom: 1.25rem; transform: translateX(-50%);
    z-index: 950;
    display: flex; align-items: center; gap: 0.6rem;
    padding: 0.55rem 0.7rem 0.55rem 1rem;
    background: var(--bg-elevated); border: 1px solid var(--border-strong);
    border-radius: 12px; box-shadow: 0 12px 32px var(--shadow);
  }
  .sel-count { font-size: 0.8rem; color: var(--text-secondary); margin-right: 0.4rem; white-space: nowrap; }
  .select-bar .sel-done { width: auto; padding: 0 0.8rem; font-size: 0.78rem; }

  /* Card + its ⋯ menu. The menu sits in the footer's right edge, outside
     the link, so the title gets room reserved for it. */
  .item-cell { position: relative; min-width: 0; display: flex; flex-direction: column; }
  .item-cell.has-menu .item-foot { padding-right: 1.7rem; }
  .item-menu { position: absolute; right: 0; bottom: 0; }

  .browse-links { display: flex; gap: 0.75rem; align-items: center; }
  .browse-links a {
    font-size: 0.78rem; color: var(--text-secondary); text-decoration: none;
    padding: 0.3rem 0.55rem; border-radius: 6px; border: 1px solid var(--border);
    transition: background 0.15s, color 0.15s, border-color 0.15s;
  }
  .browse-links a:hover { background: var(--bg-hover); color: var(--text-primary); border-color: var(--border-strong); }

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
  .music-grid {
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 1.25rem;
  }

  .item { display: flex; flex-direction: column; text-decoration: none; color: inherit; }

  .photo-grid .poster {
    aspect-ratio: 4/3;
  }
  .photo-grid .poster img {
    object-fit: cover;
  }

  .music-grid .poster,
  .item.circle-poster .poster {
    aspect-ratio: 1 / 1;
    border-radius: 50%;
  }
  .music-grid .item-foot {
    text-align: center;
  }

  /* Albums index: square covers, artist · year under the title. */
  .album-grid {
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 1.25rem 1rem;
  }
  .album-grid .poster,
  .album-grid .skeleton-poster {
    aspect-ratio: 1 / 1;
    border-radius: 6px;
  }
  .album-meta {
    display: flex; align-items: baseline; gap: 0.3rem; min-width: 0;
    padding: 0.1rem 0.1rem 0;
    font-size: 0.68rem; color: var(--text-muted);
  }
  .album-artist {
    min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    color: var(--text-muted); text-decoration: none;
  }
  a.album-artist:hover { color: var(--text-secondary); text-decoration: underline; }
  .album-year { flex-shrink: 0; }

  .filter-chip {
    display: inline-flex; align-items: center; gap: 0.3rem;
    padding: 0.25rem 0.35rem 0.25rem 0.6rem;
    background: rgba(124,106,247,0.1);
    border: 1px solid rgba(124,106,247,0.3);
    border-radius: 20px;
    font-size: 0.72rem; color: var(--accent-text);
    white-space: nowrap;
  }
  .filter-chip button {
    background: none; border: none; padding: 0 0.2rem; line-height: 1;
    font-size: 0.9rem; color: inherit; cursor: pointer;
  }
  .filter-chip button:hover { color: var(--text-primary); }

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

  .edit-meta-btn {
    position: absolute;
    top: 0.35rem;
    right: 0.35rem;
    background: rgba(0,0,0,0.65);
    border: none;
    border-radius: 50%;
    color: rgba(255,255,255,0.75);
    font-size: 0.85rem;
    width: 26px;
    height: 26px;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    opacity: 0;
    transition: opacity 0.15s, color 0.15s;
    line-height: 1;
    padding: 0;
    z-index: 2;
  }
  .item:hover .edit-meta-btn { opacity: 1; }
  .edit-meta-btn:hover { color: var(--accent); }

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

  .bucket-title {
    font-size: 0.95rem; font-weight: 600; color: var(--text-secondary);
    margin: 1.5rem 0 0.6rem; text-transform: uppercase; letter-spacing: 0.05em;
  }
  .bucket-title:first-child { margin-top: 0; }
  .bucket-grid { margin-bottom: 0.5rem; }

  .events-shelf { margin: 0 0 2rem; }
  .events-title {
    font-size: 0.95rem; font-weight: 600; color: var(--text-secondary);
    margin: 0 0 0.75rem; text-transform: uppercase; letter-spacing: 0.05em;
  }
  .events-row {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 1rem;
  }
  .event-card { color: inherit; text-decoration: none; min-width: 0; }
  .event-thumb {
    aspect-ratio: 16 / 9; border-radius: 8px; overflow: hidden;
    background: var(--bg-elevated);
  }
  .event-thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .event-thumb-blank {
    width: 100%; height: 100%; display: flex; align-items: center;
    justify-content: center; color: var(--text-muted); font-size: 1.8rem; font-weight: 700;
    background: linear-gradient(135deg, var(--bg-secondary), var(--bg-primary));
  }
  .event-name {
    padding: 0.45rem 0.1rem 0; font-size: 0.82rem; color: var(--text-primary);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }

  .mv-shelf { margin: 0 0 2rem; }
  .mv-shelf-title {
    font-size: 0.95rem; font-weight: 600; color: var(--text-secondary);
    margin: 0 0 0.75rem; text-transform: uppercase; letter-spacing: 0.05em;
  }
  .mv-row {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
    gap: 0.9rem;
  }
  .mv-card { color: inherit; text-decoration: none; min-width: 0; }
  .mv-thumb {
    position: relative; aspect-ratio: 16 / 9; border-radius: 6px;
    overflow: hidden; background: var(--bg-elevated);
  }
  .mv-thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .mv-thumb-blank {
    width: 100%; height: 100%; display: flex; align-items: center;
    justify-content: center; color: var(--text-muted); font-size: 1.5rem;
  }
  .mv-play {
    position: absolute; inset: 0; display: flex; align-items: center;
    justify-content: center; font-size: 2rem; color: white;
    background: rgba(0,0,0,0.4); opacity: 0; transition: opacity 0.15s;
  }
  .mv-card:hover .mv-play { opacity: 1; }
  .mv-meta { padding: 0.4rem 0.1rem 0; }
  .mv-title {
    font-size: 0.82rem; color: var(--text-primary);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .mv-dur { font-size: 0.7rem; color: var(--text-muted); }

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
    /* Above the bottom tab bar. */
    .select-bar { bottom: 72px; max-width: calc(100% - 1.5rem); }

    .grid {
      grid-template-columns: repeat(auto-fill, minmax(100px, 1fr));
      gap: 0.65rem;
    }

    .poster-blank span { font-size: 1.8rem; }
    .item-title { font-size: 0.68rem; }
    .item-year { font-size: 0.6rem; }
  }
</style>
