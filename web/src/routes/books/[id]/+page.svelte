<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemApi, assetUrl, getBearerToken, type ItemDetail } from '$lib/api';

  // v2.1 reader for CBZ / CBR / EPUB.
  //
  //   - CBZ + CBR: pages come from GET /items/{id}/book/page/{n},
  //     which streams the n-th alphabetically-sorted image entry
  //     from the archive.
  //   - EPUB: the whole .epub is fetched via /media/stream and
  //     handed to epub.js, which renders chapters with real
  //     reflowable pagination (font size, themes, page-flips).
  //
  // Pagination state lives in the URL query (?p=N) so a refresh /
  // share preserves the current page (CBZ/CBR) or chapter (EPUB).

  let book: ItemDetail | null = null;
  let pageNum = 1;
  let pageCount = 0;
  let loading = true;
  let error = '';
  let isAdmin = false;

  // Format dispatch — set after the item detail loads. EPUB hands
  // off to a different render path entirely (epub.js).
  let format: 'cbz' | 'cbr' | 'epub' = 'cbz';
  $: isEpub = format === 'epub';

  // EPUB state — undefined for cbz/cbr.
  let epubViewerEl: HTMLDivElement | null = null;
  let epubBook: any = null;          // ePub.Book instance
  let epubRendition: any = null;     // ePub.Rendition instance
  let epubBlobUrl: string = '';      // revoked on destroy

  // Preload the next page invisibly so a click feels instant. CBZ/CBR only.
  let preload: HTMLImageElement | null = null;

  $: id = $page.params.id!;

  onMount(async () => {
    const raw = localStorage.getItem('onscreen_user');
    if (!raw) { goto('/login'); return; }
    try { isAdmin = !!JSON.parse(raw)?.is_admin; } catch { /* keep false */ }
    pageNum = parseInt($page.url.searchParams.get('p') ?? '1', 10) || 1;
    await load();
  });

  async function removeItem() {
    if (!book) return;
    const confirmed = confirm(
      `Soft-delete "${book.title}"?\n\n` +
      `This hides the book from the library. The on-disk file is not touched.`
    );
    if (!confirmed) return;
    try {
      await itemApi.remove(book.id);
      goto(`/libraries/${book.library_id}`);
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Remove failed');
    }
  }

  onDestroy(() => {
    preload = null;
    if (epubRendition) { try { epubRendition.destroy(); } catch { /* swallow */ } epubRendition = null; }
    if (epubBook) { try { epubBook.destroy(); } catch { /* swallow */ } epubBook = null; }
    if (epubBlobUrl) { URL.revokeObjectURL(epubBlobUrl); epubBlobUrl = ''; }
  });

  $: if (id && book && id !== book.id) load();

  async function load() {
    loading = true;
    error = '';
    try {
      const detail = await itemApi.get(id);
      if (detail.type !== 'book') {
        goto(`/libraries/${detail.library_id}`, { replaceState: true });
        return;
      }
      book = detail;
      // Container is stamped from the file extension (api/v1/items.go
      // backfills it for book items when ffprobe doesn't). Falls back
      // to cbz so older rows from before this code shipped still
      // render via the image-page path.
      const c = (detail.files[0]?.container ?? 'cbz').toLowerCase();
      format = (c === 'epub' || c === 'cbr') ? c : 'cbz';
      // The scanner stashes page count on duration_ms (re-purposed —
      // see migration 00059's notes). For EPUB it's the spine length
      // (chapter count), for CBZ/CBR it's image-entry count. Fall
      // back to 1 so the reader doesn't degenerate when the page
      // count couldn't be probed.
      pageCount = Math.max(1, detail.duration_ms ?? 1);
      pageNum = Math.min(Math.max(pageNum, 1), pageCount);
      if (!isEpub) {
        schedulePreload();
      }
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load book';
    } finally {
      loading = false;
    }
  }

  // mountEpub fires once the book has loaded AND the viewer div has
  // been bound. epub.js fetches the .epub via the same /media/stream
  // URL the player uses for video — we add the bearer token as a
  // query param fallback because epub.js doesn't expose a header
  // hook on its internal XHR. The token is short-lived per session.
  $: if (book && isEpub && epubViewerEl && !epubRendition) {
    void mountEpub();
  }

  async function mountEpub() {
    if (!book || !epubViewerEl) return;
    try {
      const ePubMod = await import('epubjs');
      const ePub = (ePubMod.default ?? ePubMod) as any;
      const fileId = book.files[0]?.id;
      if (!fileId) {
        error = 'EPUB has no file';
        return;
      }
      // Fetch the .epub with our Bearer auth, then hand epub.js a
      // same-origin Blob URL of the bytes. Pure ArrayBuffer mode
      // didn't reliably wire internal resources (images, fonts,
      // stylesheets, intra-book links) into the iframe — chapter
      // pages rendered as broken-image placeholders. With a Blob
      // URL, epub.js range-requests the archive itself and resolves
      // each `<img src="../OEBPS/Images/foo.jpg">` etc. through its
      // standard URL-fetch path, which actually works.
      const token = getBearerToken();
      const headers: Record<string, string> = {};
      if (token) headers['Authorization'] = 'Bearer ' + token;
      const resp = await fetch(assetUrl(`/media/stream/${fileId}`), { headers });
      if (!resp.ok) {
        error = `Failed to fetch EPUB: ${resp.status}`;
        return;
      }
      const buf = await resp.arrayBuffer();
      const blob = new Blob([buf], { type: 'application/epub+zip' });
      epubBlobUrl = URL.createObjectURL(blob);
      epubBook = ePub(epubBlobUrl);
      epubRendition = epubBook.renderTo(epubViewerEl, {
        width: '100%',
        height: '100%',
        flow: 'paginated',
        manager: 'default',
        // allow-scripts/-same-origin in the rendered iframe so
        // resource references (blob: URLs from the parent) load
        // properly inside it. Without same-origin the browser
        // refuses cross-origin blob loads even though they share
        // the parent's origin.
        allowScriptedContent: true,
      });
      // Route external links to a new tab (epub.js handles internal
      // anchors automatically — fragment + chapter-relative hrefs
      // resolve through the rendition). Without this, http:// links
      // either no-op (sandboxed iframe) or land on a blank inside
      // the reader.
      epubRendition.on('linkClicked', (href: string) => {
        if (/^https?:\/\//i.test(href)) {
          window.open(href, '_blank', 'noopener,noreferrer');
        }
      });
      // Once locations are generated we can map page-number ↔
      // location, but generation is slow on big books — kick it off
      // in the background and just rely on spine progress until it
      // finishes.
      epubBook.ready.then(() => {
        epubBook.locations.generate(1024).catch(() => {});
      });
      epubRendition.on('relocated', (loc: any) => {
        const idx = (loc?.start?.index ?? 0) + 1;
        if (idx !== pageNum) {
          pageNum = idx;
          const url = new URL(window.location.href);
          url.searchParams.set('p', String(pageNum));
          window.history.replaceState({}, '', url.toString());
        }
        if (epubBook?.spine?.length) pageCount = epubBook.spine.length;
      });
      // Initial display — honour ?p= when present (1-indexed spine).
      const start = Math.max(1, pageNum) - 1;
      const target = epubBook.spine?.get?.(start) ?? undefined;
      await epubRendition.display(target?.href);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to render EPUB';
    }
  }

  function pageURL(n: number): string {
    return `/api/v1/items/${id}/book/page/${n}`;
  }

  function go(n: number) {
    if (n < 1 || n > pageCount) return;
    if (isEpub) {
      // Prefer the rendition's own next/prev when stepping by one —
      // they advance by viewport-width, not by spine entry, so the
      // user gets actual page-flip behaviour inside long chapters.
      // For larger jumps (slider scrub, Home/End), fall through to
      // a spine-index display.
      if (epubRendition && Math.abs(n - pageNum) === 1) {
        if (n > pageNum) epubRendition.next();
        else epubRendition.prev();
        return;
      }
      if (epubBook && epubRendition) {
        const target = epubBook.spine?.get?.(n - 1);
        if (target) epubRendition.display(target.href);
      }
      return;
    }
    pageNum = n;
    const url = new URL(window.location.href);
    url.searchParams.set('p', String(n));
    window.history.replaceState({}, '', url.toString());
    schedulePreload();
  }

  // Best-effort preload of the next page. setTimeout defers past the
  // current page render so the visible image gets prioritised.
  function schedulePreload() {
    setTimeout(() => {
      if (pageNum + 1 > pageCount) return;
      const img = new Image();
      img.src = pageURL(pageNum + 1);
      preload = img;
    }, 0);
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'ArrowRight' || e.key === 'PageDown' || e.key === ' ') {
      e.preventDefault();
      go(pageNum + 1);
    } else if (e.key === 'ArrowLeft' || e.key === 'PageUp') {
      e.preventDefault();
      go(pageNum - 1);
    } else if (e.key === 'Home') {
      e.preventDefault();
      go(1);
    } else if (e.key === 'End') {
      e.preventDefault();
      go(pageCount);
    }
  }
</script>

<svelte:head><title>{book?.title ?? 'Book'} — OnScreen</title></svelte:head>
<svelte:window on:keydown={onKey} />

<div class="page">
  {#if loading}
    <p class="loading">Loading…</p>
  {:else if error}
    <p class="err">{error}</p>
  {:else if book}
    <nav class="crumb">
      <a href="/">Libraries</a>
      <span>/</span>
      <a href="/libraries/{book.library_id}">Books</a>
      <span>/</span>
      <span>{book.title}</span>
    </nav>

    <header class="topbar">
      <h1>{book.title}</h1>
      <div class="topbar-right">
        <div class="page-counter">
          {isEpub ? 'Section' : 'Page'} {pageNum} of {pageCount}
        </div>
        {#if isAdmin}
          <button class="btn-remove" on:click={removeItem}
                  title="Soft-delete this book">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="13" height="13"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
            Remove
          </button>
        {/if}
      </div>
    </header>

    <div class="reader">
      <button
        class="nav-btn left"
        on:click={() => go(pageNum - 1)}
        disabled={!isEpub && pageNum <= 1}
        aria-label="Previous page"
      >‹</button>

      <div class="page-frame">
        {#if isEpub}
          <div class="epub-viewer" bind:this={epubViewerEl}></div>
        {:else}
          {#key pageNum}
            <img class="page-img" src={pageURL(pageNum)} alt="Page {pageNum}" />
          {/key}
        {/if}
      </div>

      <button
        class="nav-btn right"
        on:click={() => go(pageNum + 1)}
        disabled={!isEpub && pageNum >= pageCount}
        aria-label="Next page"
      >›</button>
    </div>

    <footer class="bottombar">
      <input
        type="range"
        min="1"
        max={pageCount}
        value={pageNum}
        on:input={(e) => go(parseInt((e.target as HTMLInputElement).value, 10))}
        aria-label="Jump to page"
      />
      <div class="hint">← / → or PgUp / PgDn to flip · Home / End to jump</div>
    </footer>
  {/if}
</div>

<style>
  .page {
    padding: 1rem 1.25rem 2rem;
    max-width: 1400px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    height: calc(100vh - 60px);
  }
  .loading, .err { color: var(--text-muted); font-size: 0.9rem; }
  .err { color: var(--error); }

  .crumb { font-size: 0.78rem; color: var(--text-muted); margin-bottom: 0.75rem; }
  .crumb a { color: var(--text-muted); text-decoration: none; }
  .crumb a:hover { color: var(--text-secondary); }
  .crumb span { margin: 0 0.4rem; }

  .topbar {
    display: flex; justify-content: space-between; align-items: center;
    margin-bottom: 0.75rem; gap: 1rem;
  }
  .topbar h1 {
    font-size: 1.1rem; font-weight: 600; color: var(--text-primary);
    margin: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .page-counter {
    font-size: 0.85rem; color: var(--text-secondary);
    font-variant-numeric: tabular-nums;
  }
  .topbar-right { display: flex; align-items: center; gap: 0.75rem; }
  .btn-remove {
    display: inline-flex; align-items: center; gap: 0.3rem;
    background: var(--input-bg, transparent);
    border: 1px solid rgba(204,102,102,0.3);
    border-radius: 6px;
    color: #c66; font-size: 0.72rem; font-weight: 500;
    cursor: pointer; padding: 0.3rem 0.6rem;
    transition: all 0.12s;
  }
  .btn-remove:hover { color: #e88; border-color: rgba(232,136,136,0.5); background: var(--bg-hover, rgba(204,102,102,0.06)); }

  .reader {
    flex: 1;
    display: grid;
    grid-template-columns: auto 1fr auto;
    gap: 0.5rem;
    align-items: center;
    min-height: 0;
  }
  .page-frame {
    height: 100%;
    display: flex; align-items: center; justify-content: center;
    overflow: hidden;
    background: var(--bg-elevated);
    border-radius: 6px;
  }
  .page-img {
    max-height: 100%;
    max-width: 100%;
    object-fit: contain;
    user-select: none;
  }
  .epub-viewer {
    width: 100%; height: 100%;
    background: #fdfcf7;
    color: #111;
    border-radius: 6px;
    overflow: hidden;
  }
  /* epub.js injects its own iframe inside .epub-viewer; the iframe
     manages page-flip rendering, so we just give it the box. */
  .nav-btn {
    background: var(--surface);
    border: 1px solid var(--border);
    color: var(--text-primary);
    border-radius: 999px;
    width: 44px; height: 44px;
    font-size: 1.5rem; line-height: 1;
    cursor: pointer;
    display: flex; align-items: center; justify-content: center;
    transition: background 0.12s, opacity 0.12s;
  }
  .nav-btn:hover:not(:disabled) { background: var(--bg-elevated); }
  .nav-btn:disabled { opacity: 0.3; cursor: not-allowed; }

  .bottombar {
    display: flex; flex-direction: column; gap: 0.4rem;
    margin-top: 0.75rem;
  }
  .bottombar input[type=range] { width: 100%; cursor: pointer; }
  .hint {
    font-size: 0.7rem; color: var(--text-muted); text-align: center;
  }

  @media (max-width: 600px) {
    .reader { grid-template-columns: auto 1fr auto; gap: 0.25rem; }
    .nav-btn { width: 36px; height: 36px; font-size: 1.2rem; }
  }
</style>
