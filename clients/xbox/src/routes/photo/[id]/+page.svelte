<script lang="ts">
  // Full-screen photo viewer with D-pad sibling navigation. Mirrors
  // the Android PhotoViewFragment: left/right cycles through siblings
  // with wrap-around, back exits, and OK (or Play/Pause) runs a
  // slideshow (lib/slideshow).
  //
  // The photo shows at once. Its neighbours come, in this order, from the
  // listing it was opened from (the library grid hands it over with the
  // photo's place: lib/photoWindow), from its album, from its library's
  // listing around the row the server says it is on, or from the library's
  // newest photos when it is among them; a listing is read a page around the
  // photo and a page more as left / right near the edge, never the whole
  // library, and nothing more once the viewer is left.

  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { api, endpoints, Unauthorized, type ItemDetail, type ChildItem, type MediaItem } from '$lib/api';
  import { focusManager } from '$lib/focus/manager';
  import { toRemoteKey } from '$lib/focus/keys';
  import { goBack } from '$lib/nav';
  import { SLIDESHOW_HINT_MS, Slideshow, photoCommand } from '$lib/slideshow';
  import { PhotoWindow, libraryListings, takePhotoSource, type PhotoPager } from '$lib/photoWindow';
  import { defaultSortFor } from '$lib/librarySort';
  import { i18n, t } from '$lib/i18n/index.svelte';

  const initialId = $derived(page.params.id!);

  interface Sibling {
    id: string;
    posterPath: string;
  }

  let siblings = $state<Sibling[]>([]);
  let currentIndex = $state(0);
  let currentSibling = $state<Sibling | null>(null);
  let imageBlobUrl = $state('');
  let imageError = $state('');
  // The listing the neighbours are a window of (null for an album, whose
  // photos are all in `siblings`): its offset of siblings[0], and its length
  // when the server said.
  let win: PhotoWindow | null = null;
  let windowStart = $state(0);
  let windowTotal = $state<number | null>(null);
  let windowComplete = $state(true);
  let destroyed = false;
  let positionLabel = $derived(
    siblings.length < 2
      ? ''
      : windowTotal !== null
        ? `${windowStart + currentIndex + 1} / ${windowTotal}`
        : windowComplete
          ? `${currentIndex + 1} / ${siblings.length}`
          : `${windowStart + currentIndex + 1}`
  );

  // Slideshow: "▶ Slideshow" bottom-left while it runs (Android's
  // slideshow_on), and once per visit, when there are photos to cycle
  // through, a hint that OK starts one. The two share the corner; the
  // indicator wins.
  let slideshowOn = $state(false);
  let hintVisible = $state(false);
  let hintShown = false;
  let hintTimer: ReturnType<typeof setTimeout> | null = null;
  const slideshow = new Slideshow(
    () => advance(1),
    (on) => {
      slideshowOn = on;
      if (on) hintVisible = false;
    },
  );

  function maybeShowHint() {
    if (hintShown || siblings.length < 2 || slideshow.running) return;
    hintShown = true;
    hintVisible = true;
    hintTimer = setTimeout(() => (hintVisible = false), SLIDESHOW_HINT_MS);
  }

  // In-memory cache of fetched blob URLs keyed by sibling id, plus
  // prev/next prefetch so left/right navigation is instant after the
  // first paint. Avoids the visible lag of a fresh HTTP fetch per
  // keypress.
  const blobCache = new Map<string, string>();
  const inFlight = new Map<string, Promise<string>>();
  const MAX_CACHE = 11;

  function urlForSibling(sib: Sibling): string {
    const origin = api.getOrigin();
    if (!origin) return '';
    return sib.posterPath
      ? `${origin}/artwork/${encodeURI(sib.posterPath)}?w=1920`
      : `${origin}/api/v1/items/${sib.id}/image?w=1920&h=1080&fit=contain`;
  }

  async function fetchBlobUrl(sib: Sibling): Promise<string> {
    const cached = blobCache.get(sib.id);
    if (cached) return cached;
    const existing = inFlight.get(sib.id);
    if (existing) return existing;
    // These two show in the error line ("Couldn't load photo: …"), so they
    // are in the app's language; an HTTP status stays as it is.
    if (!api.getToken()) throw new Error(t('photo.notSignedIn'));
    const url = urlForSibling(sib);
    if (!url) throw new Error(t('photo.noServer'));
    const p = (async () => {
      // Route through the refresh-aware client so a token that expired
      // mid-session refreshes + retries instead of hard-failing on 401.
      const resp = await api.authedFetch(url);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const blob = await resp.blob();
      const objUrl = URL.createObjectURL(blob);
      blobCache.set(sib.id, objUrl);
      while (blobCache.size > MAX_CACHE) {
        const oldestKey = blobCache.keys().next().value;
        if (!oldestKey) break;
        const oldUrl = blobCache.get(oldestKey);
        blobCache.delete(oldestKey);
        if (oldUrl) URL.revokeObjectURL(oldUrl);
      }
      return objUrl;
    })();
    inFlight.set(sib.id, p);
    try { return await p; }
    finally { inFlight.delete(sib.id); }
  }

  async function loadCurrentImage() {
    const sib = currentSibling;
    if (!sib) return;
    imageError = '';
    const cached = blobCache.get(sib.id);
    if (cached) {
      imageBlobUrl = cached;
      void prefetchAdjacent();
      return;
    }
    try {
      const objUrl = await fetchBlobUrl(sib);
      if (currentSibling?.id === sib.id) {
        imageBlobUrl = objUrl;
      }
      void prefetchAdjacent();
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
      else if (currentSibling?.id === sib.id) {
        // Blank the previous photo, as Android does: left up, it would hide
        // the failure (the error only shows with no photo up), and OK, which
        // retries a failed photo, would seem to do nothing.
        imageBlobUrl = '';
        imageError = (e as Error).message ?? t('photo.loadFailed');
      }
    }
  }

  function prefetchAdjacent() {
    if (siblings.length < 2) return;
    const n = siblings.length;
    // A window that isn't the whole listing doesn't wrap (see advance).
    const wraps = !win || win.complete;
    const ids = [currentIndex + 1, currentIndex - 1]
      .map((i) => (wraps ? ((i % n) + n) % n : i))
      .filter((i) => i >= 0 && i < n);
    for (const i of ids) {
      const s = siblings[i];
      if (!s || blobCache.has(s.id) || inFlight.has(s.id)) continue;
      fetchBlobUrl(s).catch(() => { /* prefetch — failures recovered on real navigation */ });
    }
  }

  $effect(() => {
    void currentSibling;
    void loadCurrentImage();
  });

  function advance(delta: number) {
    if (siblings.length < 2) return;
    const n = siblings.length;
    let next = currentIndex + delta;
    if (next < 0 || next >= n) {
      // Past the window's edge: its next page first (then the step), and a
      // wrap-around only once the whole listing is in.
      if (win && !win.complete) {
        void extendWindow(next < 0 ? -1 : 1, delta);
        return;
      }
      next = ((next % n) + n) % n;
    }
    currentIndex = next;
    currentSibling = siblings[next];
    void extendWindow(delta > 0 ? 1 : -1, 0);
  }

  /** The window's page past the current photo's side, when it is near the
   *  edge; `step` is a step that met the edge, taken once the page is in. */
  async function extendWindow(dir: 1 | -1, step: number) {
    const w = win;
    if (!w) return;
    const id = currentSibling?.id;
    const changed = dir > 0 ? await w.extendAfter(currentIndex) : await w.extendBefore(currentIndex);
    if (!changed || destroyed || w !== win) return;
    showWindow(w);
    if (step !== 0 && currentSibling?.id === id) advance(step);
  }

  /** The window's photos as the neighbours, the current photo kept. */
  function showWindow(w: PhotoWindow) {
    const id = currentSibling?.id ?? initialId;
    siblings = w.items;
    windowStart = w.start;
    windowTotal = w.total;
    windowComplete = w.complete;
    currentIndex = Math.max(0, w.indexOf(id));
    currentSibling = siblings[currentIndex];
  }

  // Photo viewer has no focusable elements, so we hook the focus
  // manager's keyHandler stack directly. Returning true swallows
  // the event and prevents the manager's direction-recovery path
  // from incorrectly handling left/right (or OK clicking nothing).
  function handleKey(k: ReturnType<typeof toRemoteKey>): boolean {
    switch (photoCommand(k)) {
      case 'prev':
        advance(-1);
        slideshow.stepped();
        return true;
      case 'next':
        advance(1);
        slideshow.stepped();
        return true;
      case 'toggle':
        // OK on a photo that failed to load retries it (Android does the
        // same); otherwise it toggles the slideshow. Only while the failure
        // is on screen, so OK never does something the user can't see.
        if (k === 'enter' && imageError && !imageBlobUrl) void loadCurrentImage();
        else slideshow.toggle(siblings.length);
        return true;
      case 'start':
        slideshow.start(siblings.length);
        return true;
      case 'stop':
        slideshow.stop();
        return true;
      default:
        return false;
    }
  }

  // Stop when the app goes to the background (Home, another input): left
  // running it would fetch and decode a full-size photo every 4 s behind
  // the launcher. Play starts it again on return, as on Android.
  function onVisibility() {
    if (document.hidden) slideshow.stop();
  }

  onMount(() => {
    const offKey = focusManager.pushKeyHandler((k) => handleKey(k));
    resolveSiblings();
    const offBack = focusManager.pushBack(() => {
      goBack();
      return true;
    });
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      offKey();
      offBack();
      document.removeEventListener('visibilitychange', onVisibility);
    };
  });

  onDestroy(() => {
    destroyed = true;
    win?.stop();
    win = null;
    slideshow.stop();
    if (hintTimer) clearTimeout(hintTimer);
    for (const url of blobCache.values()) URL.revokeObjectURL(url);
    blobCache.clear();
    inFlight.clear();
  });

  // The neighbours (see the top of this file). The photo itself is up at
  // once: its own image needs no detail (urlForSibling).
  async function resolveSiblings() {
    currentSibling = { id: initialId, posterPath: '' };
    const source = takePhotoSource(initialId);
    try {
      if (source && (await openWindow(source.page, source.at))) return;
      if (destroyed) return;
      const detail = await endpoints.items.get(initialId);
      if (destroyed) return;
      if (detail.parent_id) {
        const kids = await endpoints.items.children(detail.parent_id);
        if (destroyed) return;
        const photos = kids
          .filter((k) => k.type === 'photo')
          .map((k) => ({ id: k.id, posterPath: k.poster_path ?? '' }));
        if (photos.length >= 2) {
          siblings = photos;
          currentIndex = Math.max(0, photos.findIndex((p) => p.id === initialId));
          currentSibling = photos[currentIndex];
          maybeShowHint();
          return;
        }
      }
      // No place in a listing and no album: the library's listing in the
      // grid's default order, around the row the server says the photo is
      // on, else the library's newest photos (/api/v1/photos, one read); the
      // window from the first that holds it. Otherwise it is shown on its own.
      const lib = detail.library_id;
      const order = defaultSortFor('photo');
      const listings = await libraryListings(
        lib ? () => endpoints.libraries.position(lib, initialId, order.sort, order.dir) : null,
        async (offset, limit) => {
          const r = await endpoints.libraries.listItemsPage(lib, order.sort, order.dir, '', '', limit, offset);
          return { items: r.items.map((p) => ({ id: p.id, posterPath: p.poster_path ?? '' })), total: r.total };
        },
        (offset, limit) => fetchPhotoPage(lib, limit, offset),
      );
      for (const l of listings) {
        if (destroyed || (await openWindow(l.page, l.at, l.rows))) return;
      }
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
    }
  }

  /** The window of `page` around offset `at`; true once the photo is in it
   *  (the neighbours are then the window's). */
  async function openWindow(page: PhotoPager, at: number, first?: number): Promise<boolean> {
    const w = new PhotoWindow(page);
    win = w;
    const found = await w.open(initialId, at, first);
    if (destroyed || w !== win) return true;
    if (!found) {
      win = null;
      return false;
    }
    showWindow(w);
    maybeShowHint();
    return true;
  }

  interface PhotoListItem {
    id: string;
    poster_path?: string;
  }

  async function fetchPhotoPage(
    libraryID: string,
    limit: number,
    offset: number
  ): Promise<{ items: Sibling[]; total: number | null }> {
    const resp = await api.authedFetch(
      `/api/v1/photos?library_id=${libraryID}&limit=${limit}&offset=${offset}`
    );
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    const j = await resp.json();
    const data = j?.data ?? j;
    const list: PhotoListItem[] = data?.photos ?? data ?? [];
    const total = j?.meta?.total;
    return {
      items: Array.isArray(list) ? list.map((p) => ({ id: p.id, posterPath: p.poster_path ?? '' })) : [],
      total: typeof total === 'number' && Number.isFinite(total) ? total : null,
    };
  }
</script>

<div class="viewer">
  {#if imageBlobUrl}
    <img class="photo" src={imageBlobUrl} alt="" />
  {:else if imageError}
    <div class="status">
      {$i18n('photo.loadFailedDetail', { error: imageError })}<br />{$i18n('photo.retryHint')}
    </div>
  {:else}
    <div class="status">{$i18n('common.loading')}</div>
  {/if}
  {#if slideshowOn}
    <div class="corner-status" role="status">{$i18n('photo.slideshowOn')}</div>
  {:else if hintVisible}
    <div class="corner-status" role="status">{$i18n('photo.slideshowHint')}</div>
  {/if}
  {#if positionLabel}
    <div class="position">{positionLabel}</div>
  {/if}
</div>

<style>
  .viewer {
    position: fixed;
    inset: 0;
    background: #000;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .photo {
    max-width: 100%;
    max-height: 100%;
    object-fit: contain;
  }
  .position {
    position: absolute;
    bottom: 60px;
    right: 60px;
    padding: 14px 28px;
    background: rgba(0, 0, 0, 0.55);
    color: #fff;
    font-size: var(--font-sm);
  }
  /* Bottom-LEFT, so it can't collide with the position counter. */
  .corner-status {
    position: absolute;
    bottom: 60px;
    left: 60px;
    padding: 14px 28px;
    background: rgba(0, 0, 0, 0.55);
    color: #fff;
    font-size: var(--font-sm);
  }
  .status {
    color: rgba(255, 255, 255, 0.7);
    font-size: var(--font-md);
    text-align: center;
  }
</style>
