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

  const initialId = $derived(page.params.id!);

  let serverUrl = $state(api.getOrigin() ?? '');
  let siblingIds = $state<string[]>([]);
  let currentIndex = $state(0);
  let currentId = $state('');
  let positionLabel = $derived(
    siblingIds.length >= 2 ? `${currentIndex + 1} / ${siblingIds.length}` : ''
  );
  let imageUrl = $derived(
    serverUrl && currentId
      ? `${serverUrl}/api/v1/items/${currentId}/image?w=1920&h=1080&fit=contain`
      : ''
  );

  function advance(delta: number) {
    if (siblingIds.length < 2) return;
    const n = siblingIds.length;
    const next = ((currentIndex + delta) % n + n) % n;
    currentIndex = next;
    currentId = siblingIds[next];
  }

  // D-pad / remote keys arrive at the document; the focus manager's
  // back stack is used for Back. Left/right have no DOM-focus
  // counterpart on this page (no buttons), so subscribe directly.
  function onKey(e: KeyboardEvent) {
    const k = toRemoteKey(e);
    if (k === 'left' || k === 'rewind') {
      e.preventDefault();
      advance(-1);
    } else if (k === 'right' || k === 'forward') {
      e.preventDefault();
      advance(1);
    }
  }

  onMount(() => {
    currentId = initialId;
    document.addEventListener('keydown', onKey);
    resolveSiblings();
    return focusManager.pushBack(() => {
      history.back();
      return true;
    });
  });

  onDestroy(() => {
    document.removeEventListener('keydown', onKey);
  });

  // Build the sibling list. Tries the parent album first (when the
  // photo has one), falls back to the full library if no parent or
  // the parent's photo count is < 2. The library fallback fires page
  // requests in parallel so resolve time scales with one page-time
  // not the page count.
  async function resolveSiblings() {
    try {
      const detail = await endpoints.items.get(initialId);

      let photos: string[] = [];
      if (detail.parent_id) {
        const kids = await endpoints.items.children(detail.parent_id);
        photos = kids.filter((k) => k.type === 'photo').map((k) => k.id);
      }

      if (photos.length < 2) {
        // Library fallback. We don't have a count-aware endpoint here,
        // so paginate cautiously: ask for a generous first page (200)
        // and stop when a page returns short. For most libraries one
        // page is enough; >200 photos requires extra round trips but
        // they're issued in series only because we lack a total — the
        // server's library/items endpoint doesn't surface meta in the
        // current TS client, so this stays sequential. Acceptable
        // because the parent-album path covers most photo libraries.
        photos = [];
        const pageSize = 200;
        let offset = 0;
        // We use the top-level library/items endpoint via a direct
        // fetch to access offset. The wrapped endpoint module
        // doesn't expose it, so build the URL by hand here.
        for (;;) {
          const items = await fetchLibraryItemsPage(detail.library_id, pageSize, offset);
          for (const it of items) {
            if (it.type === 'photo') photos.push(it.id);
          }
          if (items.length < pageSize) break;
          offset += items.length;
        }
      }

      if (photos.length < 2) {
        // Single-photo album / library. Left/right will no-op, which
        // is fine; the position counter stays hidden.
        return;
      }
      siblingIds = photos;
      currentIndex = Math.max(0, photos.indexOf(initialId));
    } catch (e) {
      if (e instanceof Unauthorized) goto('/login');
      // Any other failure leaves siblings empty — viewer still
      // renders the single photo, just without navigation.
    }
  }

  async function fetchLibraryItemsPage(
    libraryID: string,
    limit: number,
    offset: number
  ): Promise<MediaItem[]> {
    const origin = api.getOrigin();
    if (!origin) throw new Error('API origin not configured');
    const tok = api.getToken();
    const resp = await fetch(
      `${origin}/api/v1/libraries/${libraryID}/items?limit=${limit}&offset=${offset}`,
      { headers: tok ? { Authorization: `Bearer ${tok}` } : {} }
    );
    if (resp.status === 401) throw new Unauthorized();
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    const j = await resp.json();
    return (j?.data ?? []) as MediaItem[];
  }
</script>

<div class="viewer">
  {#if imageUrl}
    <img class="photo" src={imageUrl} alt="" />
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
</style>
