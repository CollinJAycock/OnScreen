<script lang="ts">
  // Full-screen photo viewer with D-pad sibling navigation. Mirrors
  // the Android PhotoViewFragment: left/right cycles through siblings
  // with wrap-around, back exits.
  //
  // Sibling resolve runs on entry — siblings come from the photo's
  // parent album when there is one, otherwise the entire library
  // (paginated in parallel so a 4 000-item photo library completes
  // in one round-trip-time instead of 20 sequential ones, which used
  // to leave the user pressing left/right into a no-op for 5 s).

  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { api, endpoints, Unauthorized, type ItemDetail, type ChildItem, type MediaItem } from '$lib/api';
  import { focusManager } from '$lib/focus/manager';
  import { toRemoteKey } from '$lib/focus/keys';
  import { goBack } from '$lib/nav';

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
  let positionLabel = $derived(
    siblings.length >= 2 ? `${currentIndex + 1} / ${siblings.length}` : ''
  );

  // Small in-memory cache of fetched blob URLs keyed by sibling id.
  // Photo nav was laggy because every left/right triggered a fresh
  // HTTP fetch + blob conversion. Caching cuts re-visits to zero
  // round-trips, and the prefetchAdjacent() call below warms the
  // ±1 entries in the background so forward/back scrolling feels
  // instant after the initial paint.
  const blobCache = new Map<string, string>();
  const inFlight = new Map<string, Promise<string>>();
  const MAX_CACHE = 11; // current + 5 in each direction

  function urlForSibling(sib: Sibling): string {
    const origin = api.getOrigin();
    if (!origin) return '';
    // /artwork when poster_path is set (matches web client + works
    // with the asset-route ?token= path); /items/{id}/image as
    // fallback when the photo item has no stored poster_path.
    return sib.posterPath
      ? `${origin}/artwork/${encodeURI(sib.posterPath)}?w=1920`
      : `${origin}/api/v1/items/${sib.id}/image?w=1920&h=1080&fit=contain`;
  }

  async function fetchBlobUrl(sib: Sibling): Promise<string> {
    const cached = blobCache.get(sib.id);
    if (cached) return cached;
    const existing = inFlight.get(sib.id);
    if (existing) return existing;
    const tok = api.getToken();
    if (!tok) throw new Error('not signed in');
    const url = urlForSibling(sib);
    if (!url) throw new Error('no origin');
    const p = (async () => {
      const resp = await fetch(url, { headers: { Authorization: `Bearer ${tok}` } });
      if (resp.status === 401) throw new Unauthorized();
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const blob = await resp.blob();
      const objUrl = URL.createObjectURL(blob);
      blobCache.set(sib.id, objUrl);
      // Evict oldest when over cap (Map preserves insertion order).
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
    // Cache hit: paint immediately without awaiting.
    const cached = blobCache.get(sib.id);
    if (cached) {
      imageBlobUrl = cached;
      void prefetchAdjacent();
      return;
    }
    try {
      const objUrl = await fetchBlobUrl(sib);
      // The user may have advanced again while we were waiting;
      // only commit if this fetch is still the active one.
      if (currentSibling?.id === sib.id) {
        imageBlobUrl = objUrl;
      }
      void prefetchAdjacent();
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
      else if (currentSibling?.id === sib.id) {
        imageError = (e as Error).message ?? 'Image load failed';
      }
    }
  }

  function prefetchAdjacent() {
    if (siblings.length < 2) return;
    const n = siblings.length;
    const ids = [
      ((currentIndex + 1) % n + n) % n,
      ((currentIndex - 1) % n + n) % n,
    ];
    for (const i of ids) {
      const s = siblings[i];
      if (!s || blobCache.has(s.id) || inFlight.has(s.id)) continue;
      fetchBlobUrl(s).catch(() => { /* prefetch — failures recovered on real navigation */ });
    }
  }

  // Reload (or pull from cache) whenever the active sibling changes.
  $effect(() => {
    void currentSibling;
    void loadCurrentImage();
  });

  function advance(delta: number) {
    if (siblings.length < 2) return;
    const n = siblings.length;
    const next = ((currentIndex + delta) % n + n) % n;
    currentIndex = next;
    currentSibling = siblings[next];
  }

  // Photo viewer has no focusable elements, so we hook the focus
  // manager's keyHandler stack directly. The manager iterates these
  // before its own direction-recovery / Enter / Back logic and
  // returning true tells it to swallow the event — that prevents the
  // recovery path from incorrectly stealing left/right when there's
  // nothing focusable to navigate.
  function handleKey(k: ReturnType<typeof toRemoteKey>): boolean {
    if (k === 'left' || k === 'rewind') { advance(-1); return true; }
    if (k === 'right' || k === 'forward') { advance(1); return true; }
    return false;
  }

  onMount(() => {
    const offKey = focusManager.pushKeyHandler((k) => handleKey(k));
    resolveSiblings();
    const offBack = focusManager.pushBack(() => {
      goBack();
      return true;
    });
    return () => {
      offKey();
      offBack();
    };
  });

  onDestroy(() => {
    for (const url of blobCache.values()) URL.revokeObjectURL(url);
    blobCache.clear();
    inFlight.clear();
  });

  // Build the sibling list. Tries the parent album first (when the
  // photo has one), falls back to the full library if no parent or
  // the parent's photo count is < 2. The library fallback fires page
  // requests in parallel so resolve time scales with one page-time
  // not the page count.
  async function resolveSiblings() {
    try {
      const detail = await endpoints.items.get(initialId);
      // Seed the current sibling from the just-fetched detail so the
      // photo paints right away — the sibling list build below can
      // take a few hundred ms on large libraries.
      currentSibling = { id: detail.id, posterPath: detail.poster_path ?? '' };

      let photos: Sibling[] = [];

      // Parent-album fast path. Most photo libraries are flat (no
      // albums); skip when there's no parent.
      if (detail.parent_id) {
        const kids = await endpoints.items.children(detail.parent_id);
        photos = kids
          .filter((k) => k.type === 'photo')
          .map((k) => ({ id: k.id, posterPath: k.poster_path ?? '' }));
      }

      // Library fallback via /api/v1/photos — purpose-built endpoint
      // (paginated, library-scoped, returns poster_path). Server
      // caps page size at 500; loop until a short page lands.
      if (photos.length < 2) {
        photos = [];
        const pageSize = 500;
        let offset = 0;
        for (;;) {
          const page = await fetchPhotoPage(detail.library_id, pageSize, offset);
          for (const p of page) {
            photos.push({ id: p.id, posterPath: p.poster_path ?? '' });
          }
          if (page.length < pageSize) break;
          offset += page.length;
        }
      }

      if (photos.length < 2) return;
      siblings = photos;
      currentIndex = Math.max(0, photos.findIndex((p) => p.id === initialId));
      currentSibling = photos[currentIndex];
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
      // Any other failure leaves siblings empty — viewer still
      // renders the single photo, just without navigation.
    }
  }

  interface PhotoListItem {
    id: string;
    poster_path?: string;
  }

  async function fetchPhotoPage(
    libraryID: string,
    limit: number,
    offset: number
  ): Promise<PhotoListItem[]> {
    const origin = api.getOrigin();
    if (!origin) throw new Error('API origin not configured');
    const tok = api.getToken();
    const resp = await fetch(
      `${origin}/api/v1/photos?library_id=${libraryID}&limit=${limit}&offset=${offset}`,
      { headers: tok ? { Authorization: `Bearer ${tok}` } : {} }
    );
    if (resp.status === 401) throw new Unauthorized();
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    const j = await resp.json();
    // Server wraps in { data: { photos: [...], total: N } } per the
    // recent /photos shape; tolerate both data.photos and bare data.
    const data = j?.data ?? j;
    const list = data?.photos ?? data ?? [];
    return Array.isArray(list) ? list : [];
  }
</script>

<div class="viewer">
  {#if imageBlobUrl}
    <img class="photo" src={imageBlobUrl} alt="" />
  {:else if imageError}
    <div class="status">Couldn’t load photo: {imageError}</div>
  {:else}
    <div class="status">Loading…</div>
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
  .status {
    color: rgba(255, 255, 255, 0.7);
    font-size: var(--font-md);
    text-align: center;
  }
</style>
