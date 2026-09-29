<script lang="ts">
  // Photo map: a photo library's geotagged photos on an OpenStreetMap map,
  // as clustered thumbnail markers (GET /photos/map; the server applies the
  // caller's library access and content-rating ceiling). Clicking a marker
  // lists the photos at that spot in a side panel; a photo opens the viewer,
  // whose Previous / Next then walk those photos and whose close comes back
  // here, to the same view. ?library= picks the library.
  //
  // One request carries up to MAP_POINT_LIMIT points. A library with more
  // is reloaded per view as the user pans and zooms (split at the
  // antimeridian, which the server's filter can't cross).
  import { onMount, tick } from 'svelte';
  import { goto, replaceState } from '$app/navigation';
  import { page } from '$app/stores';
  import { get } from 'svelte/store';
  import { libraryApi, photoApi, assetUrl, type Library, type PhotoMapPoint } from '$lib/api';
  import { createPhotoMap, type PhotoMapHandle } from '$lib/photoMapLeaflet';
  import {
    MAP_POINT_LIMIT,
    bboxQueries,
    boundsOf,
    clusterPoints,
    inView,
    isSinglePosition,
    loadMapSelection,
    loadMapView,
    mapStatusText,
    mergeById,
    newestFirst,
    padBounds,
    pickByIds,
    saveMapSelection,
    saveMapView,
    takenRangeLabel,
    type Cluster,
  } from '$lib/photoMap';
  import { photoViewerHref } from '$lib/photoViewerContext';

  // Thumbnails listed for one marker; a world-zoom cluster can hold
  // thousands, and zooming in splits it up.
  const PANEL_LIMIT = 300;
  // Pan / zoom settles for this long before a per-view reload.
  const VIEW_RELOAD_MS = 400;

  let libraries = $state<Library[]>([]);
  let libraryId = $state('');
  // Raw state: these hold up to tens of thousands of points, which need
  // no deep reactivity.
  let points = $state.raw<PhotoMapPoint[]>([]);
  let selection = $state.raw<PhotoMapPoint[]>([]);
  let total = $state(0);
  let viewComplete = $state(true);
  let loading = $state(true);
  let error = $state('');
  let mapError = $state('');
  let mapEl = $state<HTMLDivElement>();

  let handle: PhotoMapHandle | null = null;
  let destroyed = false;
  let loadSeq = 0;
  let viewSeq = 0;
  let viewTimer: ReturnType<typeof setTimeout> | null = null;

  // Set when the library has more geotagged photos than one answer holds.
  let truncated = false;

  const status = $derived(mapStatusText(points.length, total, viewComplete));
  const shown = $derived(selection.slice(0, PANEL_LIMIT));
  const selectionRange = $derived(takenRangeLabel(selection));
  const canZoomToSelection = $derived.by(() => {
    const b = boundsOf(selection);
    return !!b && !isSinglePosition(b);
  });

  function errText(e: unknown, fallback: string): string {
    return e instanceof Error && e.message ? e.message : fallback;
  }

  function thumb(posterPath: string, w: number): string {
    return assetUrl(`/artwork/${encodeURI(posterPath)}?w=${w}`);
  }

  // Markers only for points in (a margin around) the view: the clusters
  // then scale with the screen, not the library.
  function recluster() {
    if (!handle) return;
    const view = padBounds(handle.bounds(), 0.25);
    const visible = points.filter((p) => inView(p, view));
    handle.showClusters(clusterPoints(visible, handle.zoom()));
  }

  function onViewChange() {
    if (!handle) return;
    saveMapView(libraryId, handle.view());
    recluster();
    if (truncated) {
      if (viewTimer) clearTimeout(viewTimer);
      viewTimer = setTimeout(loadView, VIEW_RELOAD_MS);
    }
  }

  function openCluster(c: Cluster<PhotoMapPoint>) {
    selection = newestFirst(c.points);
    saveMapSelection({ libraryId, ids: shown.map((p) => p.id) });
  }

  function closePanel() {
    selection = [];
    saveMapSelection(null);
  }

  function zoomToSelection() {
    const b = boundsOf(selection);
    if (b && handle) handle.fitBounds(b, 18);
  }

  // The whole library in one request; if that's cut off, the per-view
  // reloads (loadView) take over after the first move.
  async function loadLibrary(id: string, restore: boolean) {
    const mine = ++loadSeq;
    viewSeq++;
    loading = true;
    error = '';
    try {
      const r = await photoApi.map(id, { limit: MAP_POINT_LIMIT });
      if (mine !== loadSeq || destroyed) return;
      points = r.items ?? [];
      total = r.total;
      truncated = r.total > points.length;
      viewComplete = !truncated;

      const saved = restore ? loadMapSelection() : null;
      selection = saved && saved.libraryId === id ? pickByIds(points, saved.ids) : [];
      if (!selection.length) saveMapSelection(null);

      if (handle) {
        const savedView = restore ? loadMapView(id) : null;
        const b = boundsOf(points);
        if (savedView) handle.setView(savedView);
        else if (b) handle.fitBounds(b);
        else handle.setView({ lat: 20, lon: 0, zoom: 2 });
        recluster();
      }
    } catch (e) {
      if (mine === loadSeq) error = errText(e, 'Could not load the photo map');
    } finally {
      if (mine === loadSeq) loading = false;
    }
  }

  async function loadView() {
    viewTimer = null;
    if (!handle || !truncated || destroyed) return;
    const mine = ++viewSeq;
    const lib = libraryId;
    const boxes = bboxQueries(padBounds(handle.bounds(), 0.1));
    try {
      const answers = await Promise.all(
        boxes.map((b) => photoApi.map(lib, { ...b, limit: MAP_POINT_LIMIT })),
      );
      if (mine !== viewSeq || lib !== libraryId || destroyed) return;
      points = mergeById(...answers.map((a) => a.items ?? []));
      viewComplete = answers.every((a) => (a.items ?? []).length < MAP_POINT_LIMIT);
      recluster();
    } catch {
      // Keep what's on the map; the next move tries again.
    }
  }

  function setLibrary(id: string) {
    if (id === libraryId) return;
    libraryId = id;
    closePanel();
    try {
      const u = new URL(get(page).url);
      u.searchParams.set('library', id);
      replaceState(u, get(page).state);
    } catch { /* router not ready (tests / early mount) — the library still switches */ }
    loadLibrary(id, true);
  }

  async function init() {
    try {
      const libs = await libraryApi.list();
      libraries = (libs ?? []).filter((l) => l.type === 'photo');
    } catch (e) {
      error = errText(e, 'Could not load libraries');
      loading = false;
      return;
    }
    if (!libraries.length) {
      loading = false;
      return;
    }
    const wanted = get(page).url.searchParams.get('library');
    libraryId = wanted && libraries.some((l) => l.id === wanted) ? wanted : libraries[0].id;

    await tick();
    if (destroyed) return;
    if (mapEl) {
      try {
        handle = await createPhotoMap(mapEl, {
          thumbUrl: (p) => thumb(p, 96),
          onViewChange,
          onClusterClick: openCluster,
        });
      } catch (e) {
        mapError = errText(e, 'The map could not be loaded');
      }
    }
    if (destroyed) {
      handle?.destroy();
      handle = null;
      return;
    }
    await loadLibrary(libraryId, true);
  }

  onMount(() => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    init();
    return () => {
      destroyed = true;
      if (viewTimer) clearTimeout(viewTimer);
      handle?.destroy();
      handle = null;
    };
  });

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape' && selection.length) closePanel();
  }
</script>

<svelte:head><title>Photo map — OnScreen</title></svelte:head>
<svelte:window onkeydown={onKey} />

<div class="page">
  <nav class="crumb">
    <a href="/">Libraries</a>
    <span>/</span>
    {#if libraryId}
      <a href="/libraries/{libraryId}">{libraries.find((l) => l.id === libraryId)?.name ?? 'Photos'}</a>
      <span>/</span>
    {/if}
    <span>Map</span>
  </nav>

  <div class="head">
    <h1>Photo map</h1>
    {#if libraries.length > 1}
      <select
        class="lib-select"
        aria-label="Photo library"
        value={libraryId}
        onchange={(e) => setLibrary(e.currentTarget.value)}
      >
        {#each libraries as l (l.id)}
          <option value={l.id}>{l.name}</option>
        {/each}
      </select>
    {/if}
    <a class="head-link" href="/photos/albums">Albums</a>
    {#if libraries.length && !error}
      <div class="status" role="status">{loading ? 'Loading…' : status}</div>
    {/if}
  </div>

  {#if error}
    <div class="error-bar" role="alert">{error}</div>
  {/if}

  {#if !loading && !error && libraries.length === 0}
    <div class="empty">
      <p class="empty-t">No photo libraries</p>
      <p class="empty-s">Add a Photos library to see its geotagged photos on a map.</p>
    </div>
  {:else if libraries.length}
    <div class="map-wrap">
      <div class="map" bind:this={mapEl}></div>

      {#if mapError}
        <div class="map-msg" role="alert">{mapError}</div>
      {:else if !loading && !error && total === 0}
        <div class="map-msg">
          <strong>No geotagged photos</strong>
          <span>Photos with GPS coordinates in their EXIF data appear here.</span>
        </div>
      {/if}

      {#if selection.length}
        <aside class="panel" aria-label="Photos at this spot">
          <header class="panel-head">
            <div class="panel-title">
              <h2>{selection.length === 1 ? selection[0].title : `${selection.length.toLocaleString()} photos`}</h2>
              {#if selectionRange}<div class="panel-sub">{selectionRange}</div>{/if}
            </div>
            {#if canZoomToSelection}
              <button type="button" class="panel-btn" onclick={zoomToSelection}>Zoom in</button>
            {/if}
            <button type="button" class="panel-close" aria-label="Close" onclick={closePanel}>×</button>
          </header>
          <div class="thumbs">
            {#each shown as p (p.id)}
              <a class="thumb" href={photoViewerHref(p.id, { kind: 'map', libraryId })} title={p.title}>
                {#if p.poster_path}
                  <img src={thumb(p.poster_path, 300)} alt={p.title} loading="lazy" />
                {:else}
                  <span class="thumb-blank">{p.title[0]?.toUpperCase() ?? '◇'}</span>
                {/if}
              </a>
            {/each}
          </div>
          {#if selection.length > shown.length}
            <p class="panel-more">
              Showing {shown.length} of {selection.length.toLocaleString()}. Zoom in to split this spot up.
            </p>
          {/if}
        </aside>
      {/if}
    </div>
  {/if}
</div>

<style>
  .page { padding: 2.5rem 2.5rem 1.5rem; }

  .crumb {
    display: flex; align-items: center; gap: 0.4rem;
    font-size: 0.75rem; color: var(--text-muted); margin-bottom: 1.25rem;
  }
  .crumb a { color: var(--text-muted); text-decoration: none; }
  .crumb a:hover { color: var(--text-secondary); }

  .head { display: flex; align-items: center; gap: 0.75rem; flex-wrap: wrap; margin-bottom: 1rem; }
  h1 { font-size: 1.4rem; font-weight: 800; color: var(--text-primary); letter-spacing: -0.025em; }
  .lib-select {
    background: var(--bg-hover); border: 1px solid var(--border-strong); border-radius: 7px;
    padding: 0.35rem 0.6rem; font-size: 0.75rem; color: var(--text-secondary); cursor: pointer;
  }
  .lib-select:focus { outline: none; border-color: var(--accent); }
  .lib-select option { background: var(--bg-elevated); color: var(--text-primary); }
  .head-link {
    font-size: 0.78rem; color: var(--text-secondary); text-decoration: none;
    padding: 0.3rem 0.55rem; border-radius: 6px; border: 1px solid var(--border);
  }
  .head-link:hover { background: var(--bg-hover); color: var(--text-primary); border-color: var(--border-strong); }
  .status { margin-left: auto; font-size: 0.75rem; color: var(--text-muted); }

  .error-bar {
    background: var(--error-bg); color: var(--error); padding: 0.6rem 0.9rem;
    border-radius: 8px; font-size: 0.8rem; margin-bottom: 1rem;
  }

  /* isolation keeps Leaflet's pane / control z-indices (400-1000) inside
     the map instead of competing with the app's overlays. */
  .map-wrap {
    position: relative;
    isolation: isolate;
    height: calc(100vh - 11rem);
    height: calc(100dvh - 11rem);
    min-height: 360px;
    border-radius: 10px;
    overflow: hidden;
    border: 1px solid var(--border);
    background: var(--bg-elevated);
  }
  .map { position: absolute; inset: 0; }

  .map-msg {
    position: absolute; left: 50%; top: 1rem; transform: translateX(-50%);
    z-index: 1100;
    display: flex; flex-direction: column; align-items: center; gap: 0.2rem;
    padding: 0.6rem 1rem; border-radius: 8px;
    background: var(--bg-elevated); border: 1px solid var(--border-strong);
    box-shadow: 0 8px 24px var(--shadow);
    font-size: 0.8rem; color: var(--text-secondary); text-align: center;
  }
  .map-msg strong { color: var(--text-primary); }

  .panel {
    position: absolute; top: 0; right: 0; bottom: 0;
    z-index: 1100;
    width: 340px; max-width: 90%;
    display: flex; flex-direction: column;
    background: var(--bg-elevated);
    border-left: 1px solid var(--border-strong);
    box-shadow: -8px 0 24px var(--shadow);
  }
  .panel-head {
    display: flex; align-items: flex-start; gap: 0.5rem;
    padding: 0.8rem 0.9rem; border-bottom: 1px solid var(--border);
  }
  .panel-title { flex: 1; min-width: 0; }
  .panel-title h2 {
    font-size: 0.9rem; font-weight: 700; color: var(--text-primary);
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .panel-sub { font-size: 0.72rem; color: var(--text-muted); margin-top: 0.15rem; }
  .panel-btn {
    padding: 0.3rem 0.6rem; background: var(--accent-bg);
    border: 1px solid rgba(124,106,247,0.25); border-radius: 6px;
    color: var(--accent-text); font-size: 0.72rem; font-weight: 600; cursor: pointer; white-space: nowrap;
  }
  .panel-btn:hover { background: rgba(124,106,247,0.2); }
  .panel-close {
    background: none; border: none; color: var(--text-muted);
    font-size: 1.2rem; line-height: 1; cursor: pointer; padding: 0.1rem 0.2rem;
  }
  .panel-close:hover { color: var(--text-primary); }

  .thumbs {
    flex: 1; overflow-y: auto; padding: 0.6rem;
    display: grid; grid-template-columns: repeat(auto-fill, minmax(96px, 1fr));
    gap: 0.4rem; align-content: start;
  }
  .thumb {
    aspect-ratio: 1; border-radius: 6px; overflow: hidden;
    background: var(--bg-hover); display: block;
  }
  .thumb img { width: 100%; height: 100%; object-fit: cover; display: block; transition: transform 0.2s; }
  .thumb:hover img { transform: scale(1.05); }
  .thumb:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .thumb-blank {
    width: 100%; height: 100%; display: flex; align-items: center; justify-content: center;
    font-size: 1.4rem; font-weight: 800; color: var(--text-muted);
  }
  .panel-more { padding: 0.5rem 0.9rem 0.8rem; font-size: 0.72rem; color: var(--text-muted); }

  .empty { text-align: center; padding: 3.5rem 1rem; color: var(--text-muted); }
  .empty-t { font-weight: 700; color: var(--text-secondary); }
  .empty-s { font-size: 0.8rem; margin-top: 0.4rem; }

  /* Markers are built by the Leaflet adapter, outside this component's
     scope. Leaflet sizes the .pm-icon box; the thumbnail fills it. */
  .map-wrap :global(.pm-icon) { background: none; border: none; }
  .map-wrap :global(.pm-marker) {
    position: relative; width: 100%; height: 100%;
    border-radius: 50%;
    border: 2px solid #fff;
    box-shadow: 0 2px 8px rgba(0,0,0,0.45);
    background: #7c6af7;
    cursor: pointer;
  }
  .map-wrap :global(.pm-marker img) {
    width: 100%; height: 100%; border-radius: 50%; object-fit: cover; display: block;
  }
  .map-wrap :global(.pm-cluster) { border-color: #7c6af7; }
  .map-wrap :global(.pm-count) {
    position: absolute; right: -8px; top: -6px;
    min-width: 20px; height: 20px; padding: 0 5px;
    border-radius: 10px; background: #7c6af7; color: #fff;
    font-size: 0.66rem; font-weight: 700; line-height: 20px; text-align: center;
    box-shadow: 0 1px 4px rgba(0,0,0,0.4);
  }
  .map-wrap :global(.leaflet-marker-icon:focus-visible .pm-marker) {
    outline: 3px solid #fff; outline-offset: 2px;
  }

  @media (max-width: 768px) {
    .page { padding: 1.25rem 1rem 1rem; }
    .map-wrap { height: calc(100vh - 15rem); height: calc(100dvh - 15rem); }
    .status { margin-left: 0; width: 100%; }
    /* Bottom sheet on phones. */
    .panel {
      top: auto; left: 0; width: 100%; max-width: none; height: 55%;
      border-left: none; border-top: 1px solid var(--border-strong);
      box-shadow: 0 -8px 24px var(--shadow);
    }
  }
</style>
