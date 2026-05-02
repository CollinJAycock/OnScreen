<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemApi, mediaApi, assetUrl, type ItemDetail, type MediaItem, type PhotoEXIF } from '$lib/api';
  import MetadataEditor from '$lib/components/MetadataEditor.svelte';

  let item: ItemDetail | null = null;
  let exif: PhotoEXIF | null = null;
  let loading = true;
  let error = '';
  let isAdmin = false;
  let editMetadataOpen = false;

  // Sibling photos in the same library, used for prev/next nav.
  let siblings: MediaItem[] = [];
  let siblingIdx = -1;

  let showInfo = false;
  let zoom = 1;
  let panX = 0;
  let panY = 0;
  let dragStartX = 0;
  let dragStartY = 0;
  let isDragging = false;

  // Slideshow state — auto-advances every SLIDESHOW_MS until user stops it
  // or runs off the end. UI hides while slideshow is running.
  let slideshow = false;
  const SLIDESHOW_MS = 4000;
  let slideTimer: ReturnType<typeof setTimeout> | null = null;

  $: id = $page.params.id!;
  $: prevPhoto = siblingIdx > 0 ? siblings[siblingIdx - 1] : null;
  $: nextPhoto = siblingIdx >= 0 && siblingIdx < siblings.length - 1 ? siblings[siblingIdx + 1] : null;

  async function load(currentId: string) {
    loading = true;
    error = '';
    exif = null;
    item = null;
    resetZoom();
    try {
      item = await itemApi.get(currentId);
      // EXIF is optional — 404 just means no EXIF block on this image.
      itemApi.exif(currentId).then(e => { if (id === currentId) exif = e; }).catch(() => {});
      // Load siblings whenever the current photo isn't already in the cached
      // list (first visit, or jumped to a photo in a different library).
      // Photo libraries default to taken_at desc, matching browse order.
      if (item && !siblings.some(s => s.id === currentId)) {
        try {
          const r = await mediaApi.listItems(item.library_id, 500, 0, { sort: 'taken_at', sort_dir: 'desc' });
          siblings = r.items;
        } catch { /* nav fallback */ }
      }
      siblingIdx = siblings.findIndex(s => s.id === currentId);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load photo';
    } finally {
      loading = false;
    }
  }

  function go(p: MediaItem | null) {
    if (!p) return;
    goto(`/photos/${p.id}`);
  }

  // Always escape to the parent library — per-photo navigations push history
  // entries, so history.back() would just step to the previous photo instead
  // of leaving the viewer.
  function close() {
    if (item?.library_id) {
      goto(`/libraries/${item.library_id}`);
    } else {
      goto('/');
    }
  }

  function clearSlideTimer() {
    if (slideTimer) { clearTimeout(slideTimer); slideTimer = null; }
  }

  function scheduleNextSlide() {
    clearSlideTimer();
    if (!slideshow) return;
    slideTimer = setTimeout(() => {
      if (!slideshow) return;
      if (nextPhoto) go(nextPhoto);
      else slideshow = false;
    }, SLIDESHOW_MS);
  }

  function toggleSlideshow() {
    slideshow = !slideshow;
    if (slideshow) {
      resetZoom();
      showInfo = false;
      scheduleNextSlide();
    } else {
      clearSlideTimer();
    }
  }

  function resetZoom() {
    zoom = 1;
    panX = 0;
    panY = 0;
  }

  function zoomIn() { zoom = Math.min(zoom * 1.5, 8); }
  function zoomOut() {
    zoom = Math.max(zoom / 1.5, 1);
    if (zoom === 1) { panX = 0; panY = 0; }
  }

  function onWheel(e: WheelEvent) {
    e.preventDefault();
    if (e.deltaY < 0) zoomIn();
    else zoomOut();
  }

  function onDblClick() {
    if (zoom === 1) zoom = 2;
    else resetZoom();
  }

  function onMouseDown(e: MouseEvent) {
    if (zoom === 1) return;
    isDragging = true;
    dragStartX = e.clientX - panX;
    dragStartY = e.clientY - panY;
  }
  function onMouseMove(e: MouseEvent) {
    if (!isDragging) return;
    panX = e.clientX - dragStartX;
    panY = e.clientY - dragStartY;
  }
  function onMouseUp() { isDragging = false; }

  // Touch swipe nav (when not zoomed) + pan (when zoomed).
  let touchStartX = 0;
  let touchStartY = 0;
  let touchStartTime = 0;
  let initialDistance = 0;
  let initialZoom = 1;

  function distance(t: TouchList): number {
    if (t.length < 2) return 0;
    const dx = t[0].clientX - t[1].clientX;
    const dy = t[0].clientY - t[1].clientY;
    return Math.hypot(dx, dy);
  }

  function onTouchStart(e: TouchEvent) {
    if (e.touches.length === 2) {
      initialDistance = distance(e.touches);
      initialZoom = zoom;
      return;
    }
    touchStartX = e.touches[0].clientX;
    touchStartY = e.touches[0].clientY;
    touchStartTime = Date.now();
    if (zoom > 1) {
      dragStartX = touchStartX - panX;
      dragStartY = touchStartY - panY;
    }
  }

  function onTouchMove(e: TouchEvent) {
    if (e.touches.length === 2 && initialDistance > 0) {
      e.preventDefault();
      const d = distance(e.touches);
      zoom = Math.max(1, Math.min(8, initialZoom * (d / initialDistance)));
      if (zoom === 1) { panX = 0; panY = 0; }
      return;
    }
    if (zoom > 1) {
      e.preventDefault();
      panX = e.touches[0].clientX - dragStartX;
      panY = e.touches[0].clientY - dragStartY;
    }
  }

  function onTouchEnd(e: TouchEvent) {
    if (zoom > 1) return; // pan ended
    if (e.changedTouches.length === 0) return;
    const dx = e.changedTouches[0].clientX - touchStartX;
    const dy = e.changedTouches[0].clientY - touchStartY;
    const dt = Date.now() - touchStartTime;
    // Horizontal swipe — short, mostly horizontal, fast.
    if (dt < 500 && Math.abs(dx) > 60 && Math.abs(dx) > Math.abs(dy) * 1.5) {
      if (dx < 0) go(nextPhoto);
      else go(prevPhoto);
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'ArrowLeft') { e.preventDefault(); go(prevPhoto); }
    else if (e.key === 'ArrowRight') { e.preventDefault(); go(nextPhoto); }
    else if (e.key === 'Escape') {
      if (slideshow) { slideshow = false; clearSlideTimer(); }
      else if (showInfo) { showInfo = false; }
      else close();
    }
    else if (e.key === 'i' || e.key === 'I') { showInfo = !showInfo; }
    else if (e.key === '+' || e.key === '=') { e.preventDefault(); zoomIn(); }
    else if (e.key === '-') { e.preventDefault(); zoomOut(); }
    else if (e.key === '0') { resetZoom(); }
    else if (e.key === ' ') { e.preventDefault(); toggleSlideshow(); }
  }

  onMount(() => {
    const raw = localStorage.getItem('onscreen_user');
    if (!raw) { goto('/login'); return; }
    try { isAdmin = !!JSON.parse(raw)?.is_admin; } catch { /* keep false */ }
    load(id);
    window.addEventListener('keydown', onKey);
  });
  onDestroy(() => {
    window.removeEventListener('keydown', onKey);
    clearSlideTimer();
  });

  // Reload when navigating between photos (URL changes). Reschedule slideshow
  // for the new photo so each slide gets its full dwell.
  let lastId = '';
  $: if (id && id !== lastId) {
    lastId = id;
    load(id);
    if (slideshow) scheduleNextSlide();
  }

  function fmtTaken(s?: string): string {
    if (!s) return '';
    try { return new Date(s).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' }); }
    catch { return s; }
  }
  function fmtFocal(mm?: number): string { return mm ? `${mm.toFixed(0)}mm` : ''; }
  function fmtAperture(a?: number): string { return a ? `f/${a.toFixed(1)}` : ''; }
  function fmtISO(iso?: number): string { return iso ? `ISO ${iso}` : ''; }
  function fmtGPS(lat?: number, lon?: number): string {
    if (lat == null || lon == null) return '';
    return `${lat.toFixed(4)}, ${lon.toFixed(4)}`;
  }
</script>

<svelte:head><title>{item?.title ?? 'Photo'} — OnScreen</title></svelte:head>

<svelte:window on:mousemove={onMouseMove} on:mouseup={onMouseUp} />

<div class="page" class:dragging={isDragging} class:slideshow-on={slideshow}>
  <header class="bar">
    <button class="icon-btn" on:click={close} title="Close (Esc)" aria-label="Close">✕</button>
    <div class="title-block">
      <div class="title">{item?.title ?? ''}</div>
      {#if exif?.taken_at}
        <div class="subtitle">{fmtTaken(exif.taken_at)}</div>
      {/if}
    </div>
    <div class="bar-spacer"></div>
    <button class="icon-btn" on:click={zoomOut} disabled={zoom <= 1} title="Zoom out (-)">−</button>
    <span class="zoom-pct">{Math.round(zoom * 100)}%</span>
    <button class="icon-btn" on:click={zoomIn} disabled={zoom >= 8} title="Zoom in (+)">+</button>
    <button
      class="icon-btn"
      class:on={slideshow}
      on:click={toggleSlideshow}
      disabled={siblings.length < 2}
      title="Slideshow (Space)"
    >{slideshow ? '⏸' : '▶'}</button>
    <button class="icon-btn" class:on={showInfo} on:click={() => showInfo = !showInfo} title="Info (i)">i</button>
    {#if isAdmin}
      <button class="icon-btn" on:click={() => editMetadataOpen = true} title="Edit metadata" aria-label="Edit metadata">✎</button>
    {/if}
  </header>

  {#if loading && !item}
    <div class="state">Loading…</div>
  {:else if error}
    <div class="state error">{error}</div>
  {:else if item && item.poster_path}
    <div
      class="frame"
      on:wheel={onWheel}
      on:dblclick={onDblClick}
      on:mousedown={onMouseDown}
      on:touchstart={onTouchStart}
      on:touchmove={onTouchMove}
      on:touchend={onTouchEnd}
      role="presentation"
    >
      <img
        src={assetUrl(`/artwork/${encodeURI(item.poster_path)}?v=${item.updated_at}`)}
        alt={item.title}
        draggable="false"
        style="transform: translate({panX}px, {panY}px) scale({zoom}); cursor: {zoom > 1 ? (isDragging ? 'grabbing' : 'grab') : 'zoom-in'};"
      />
    </div>

    {#if prevPhoto}
      <button class="nav-btn left" on:click={() => go(prevPhoto)} title="Previous (←)">‹</button>
    {/if}
    {#if nextPhoto}
      <button class="nav-btn right" on:click={() => go(nextPhoto)} title="Next (→)">›</button>
    {/if}

    {#if siblings.length > 0 && siblingIdx >= 0}
      <div class="counter">{siblingIdx + 1} / {siblings.length}</div>
    {/if}

    {#if showInfo}
      <aside class="info">
        <h3>{item.title}</h3>
        {#if exif?.taken_at}<div class="row"><span class="k">Taken</span><span class="v">{fmtTaken(exif.taken_at)}</span></div>{/if}
        {#if exif?.camera_make || exif?.camera_model}
          <div class="row"><span class="k">Camera</span><span class="v">{[exif.camera_make, exif.camera_model].filter(Boolean).join(' ')}</span></div>
        {/if}
        {#if exif?.lens_model}<div class="row"><span class="k">Lens</span><span class="v">{exif.lens_model}</span></div>{/if}
        {#if exif?.focal_length_mm || exif?.aperture || exif?.shutter_speed || exif?.iso}
          <div class="row">
            <span class="k">Exposure</span>
            <span class="v">
              {[fmtFocal(exif.focal_length_mm), fmtAperture(exif.aperture), exif.shutter_speed ? `${exif.shutter_speed}s` : '', fmtISO(exif.iso)].filter(Boolean).join(' · ')}
            </span>
          </div>
        {/if}
        {#if exif?.width && exif?.height}
          <div class="row"><span class="k">Size</span><span class="v">{exif.width} × {exif.height}</span></div>
        {/if}
        {#if exif?.gps_lat != null && exif?.gps_lon != null}
          <div class="row">
            <span class="k">Location</span>
            <span class="v">
              <a href="https://www.openstreetmap.org/?mlat={exif.gps_lat}&mlon={exif.gps_lon}#map=15/{exif.gps_lat}/{exif.gps_lon}" target="_blank" rel="noopener noreferrer">{fmtGPS(exif.gps_lat, exif.gps_lon)}</a>
            </span>
          </div>
        {/if}
        {#if !exif}
          <div class="row muted">No EXIF data</div>
        {/if}
      </aside>
    {/if}
  {:else}
    <div class="state">Photo not available</div>
  {/if}
</div>

{#if item}
  <MetadataEditor
    itemId={item.id}
    initialTitle={item.title}
    initialSummary={item.summary}
    initialTakenAt={item.taken_at}
    open={editMetadataOpen}
    on:close={() => editMetadataOpen = false}
    on:saved={(e) => {
      if (item) {
        item = { ...item, title: e.detail.title, summary: e.detail.summary ?? undefined, taken_at: e.detail.taken_at ?? undefined };
      }
    }}
  />
{/if}

<style>
  .page {
    position: fixed;
    inset: 0;
    background: #000;
    color: #fff;
    overflow: hidden;
    user-select: none;
  }
  .page.dragging { cursor: grabbing; }
  /* In slideshow mode, fade out chrome but leave it focusable on hover. */
  .page.slideshow-on .bar,
  .page.slideshow-on .nav-btn,
  .page.slideshow-on .counter {
    opacity: 0;
    transition: opacity 0.4s;
    pointer-events: none;
  }
  .page.slideshow-on:hover .bar,
  .page.slideshow-on:hover .nav-btn,
  .page.slideshow-on:hover .counter {
    opacity: 1;
    pointer-events: auto;
  }
  .page.slideshow-on .bar > * { pointer-events: auto; }

  .bar {
    position: absolute;
    top: 0; left: 0; right: 0;
    z-index: 10;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.6rem 0.9rem;
    background: linear-gradient(to bottom, rgba(0,0,0,0.7), transparent);
    pointer-events: none;
  }
  .bar > * { pointer-events: auto; }
  .bar-spacer { flex: 1; }

  .title-block { display: flex; flex-direction: column; min-width: 0; }
  .title { font-size: 0.9rem; font-weight: 600; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .subtitle { font-size: 0.72rem; color: rgba(255,255,255,0.6); }

  .icon-btn {
    width: 32px; height: 32px;
    background: rgba(255,255,255,0.1);
    border: 1px solid rgba(255,255,255,0.15);
    border-radius: 6px;
    color: #fff;
    font-size: 0.95rem;
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    transition: background 0.12s;
  }
  .icon-btn:hover:not(:disabled) { background: rgba(255,255,255,0.2); }
  .icon-btn:disabled { opacity: 0.35; cursor: not-allowed; }
  .icon-btn.on { background: rgba(124,106,247,0.4); border-color: rgba(124,106,247,0.6); }

  .zoom-pct {
    font-size: 0.72rem;
    color: rgba(255,255,255,0.7);
    min-width: 38px;
    text-align: center;
    font-variant-numeric: tabular-nums;
  }

  .frame {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    overflow: hidden;
    touch-action: none;
  }
  .frame img {
    max-width: 100vw;
    max-height: 100vh;
    object-fit: contain;
    display: block;
    transition: transform 0.18s ease-out;
    will-change: transform;
  }
  .page.dragging .frame img { transition: none; }

  .nav-btn {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    width: 44px;
    height: 64px;
    background: rgba(0,0,0,0.5);
    border: none;
    color: #fff;
    font-size: 2rem;
    line-height: 1;
    cursor: pointer;
    border-radius: 6px;
    opacity: 0.4;
    transition: opacity 0.15s, background 0.15s;
    z-index: 5;
  }
  .nav-btn:hover { opacity: 1; background: rgba(0,0,0,0.75); }
  .nav-btn.left { left: 0.6rem; }
  .nav-btn.right { right: 0.6rem; }

  .counter {
    position: absolute;
    bottom: 1rem;
    left: 50%;
    transform: translateX(-50%);
    background: rgba(0,0,0,0.55);
    padding: 0.3rem 0.7rem;
    border-radius: 999px;
    font-size: 0.72rem;
    color: rgba(255,255,255,0.8);
    font-variant-numeric: tabular-nums;
  }

  .info {
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    width: 320px;
    max-width: 88vw;
    background: rgba(20,20,22,0.95);
    backdrop-filter: blur(12px);
    border-left: 1px solid rgba(255,255,255,0.1);
    padding: 4rem 1.25rem 1.5rem;
    overflow-y: auto;
    z-index: 8;
  }
  .info h3 {
    font-size: 0.95rem;
    font-weight: 600;
    margin-bottom: 1rem;
    word-break: break-word;
  }
  .row {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    margin-bottom: 0.85rem;
    font-size: 0.78rem;
  }
  .row .k {
    color: rgba(255,255,255,0.45);
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }
  .row .v { color: rgba(255,255,255,0.92); }
  .row.muted { color: rgba(255,255,255,0.45); }
  .row a { color: #a78bfa; text-decoration: none; }
  .row a:hover { text-decoration: underline; }

  .state {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(255,255,255,0.6);
    font-size: 0.9rem;
  }
  .state.error { color: #f87171; }

  @media (max-width: 600px) {
    .info { width: 88vw; }
    .nav-btn { width: 36px; height: 54px; font-size: 1.6rem; }
  }
</style>
