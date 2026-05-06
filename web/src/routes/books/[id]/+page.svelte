<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemApi, assetUrl, getBearerToken, type ItemDetail } from '$lib/api';
  import {
    effectiveLayout as computeEffectiveLayout,
    pageStep as computePageStep,
    keyToPageDelta,
    keyToAbsolutePage,
    type ReadingDirection,
    type LayoutMode,
  } from './reader-nav';

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

  // Reader modes for manga / comics.
  //
  //   direction: 'ltr' = Western (left → right), 'rtl' = manga
  //              (right → left), 'ttb' = webtoon (vertical scroll).
  //   layout:    'single' = one page at a time (default),
  //              'spread' = two pages side-by-side (manga / comics),
  //              'scroll' = all pages stacked vertically (webtoons).
  //
  // Defaults: direction picks up book.reading_direction when set
  // (manga enricher populates from AniList countryOfOrigin) and
  // falls back to ltr; layout always starts at 'single'. ttb
  // implies 'scroll' (webtoons aren't read page-by-page) and
  // overrides layout. EPUB ignores both — epub.js owns its own
  // pagination.
  let direction: ReadingDirection = 'ltr';
  let layout: LayoutMode = 'single';
  // Reactive view of computeEffectiveLayout — ttb forces scroll; otherwise
  // pass-through. Keeping this as $: so Svelte tracks both inputs.
  $: effectiveLayout = computeEffectiveLayout(direction, layout);

  // EPUB state — undefined for cbz/cbr.
  let epubViewerEl: HTMLDivElement | null = null;
  let epubBook: any = null;          // ePub.Book instance
  let epubRendition: any = null;     // ePub.Rendition instance

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
      // Reading direction: prefer the per-book override stored by the
      // manga enricher (AniList countryOfOrigin → JP=rtl, KR/CN=ttb),
      // fall back to ltr for ordinary books / EPUB. The reader's UI
      // controls let the user override at view time without writing
      // back to the row.
      if (detail.reading_direction === 'rtl' || detail.reading_direction === 'ttb' || detail.reading_direction === 'ltr') {
        direction = detail.reading_direction;
      } else {
        direction = 'ltr';
      }
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
      // Read `format` directly here, not the `isEpub` reactive — `$:`
      // declarations don't update mid-function, so reading isEpub
      // would see the stale false from initial render and the
      // prefetcher would 404 against /book/page/N for an EPUB.
      if (format !== 'epub') {
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
      // Fetch the .epub via /media/stream with our Bearer auth and
      // hand the bytes to epub.js as an ArrayBuffer. Resources
      // inside the archive (images, fonts, stylesheets) get served
      // out of the in-memory archive map; the rendition's iframe
      // needs allowScriptedContent (which translates to allow-
      // scripts + allow-same-origin in the sandbox attribute) for
      // those internal blob: references to actually load — without
      // it Chromium silently blocks them and chapters render with
      // broken-image placeholders.
      const token = getBearerToken();
      const headers: Record<string, string> = {};
      if (token) headers['Authorization'] = 'Bearer ' + token;
      const resp = await fetch(assetUrl(`/media/stream/${fileId}`), { headers });
      if (!resp.ok) {
        error = `Failed to fetch EPUB: ${resp.status}`;
        return;
      }
      const buf = await resp.arrayBuffer();
      epubBook = ePub(buf);
      epubRendition = epubBook.renderTo(epubViewerEl, {
        width: '100%',
        height: '100%',
        flow: 'paginated',
        manager: 'default',
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

  // Navigation step. Spread mode advances by 2 to keep both pages
  // turning together; ttb / scroll ignore go() entirely (the page
  // is rendered as one long column, browser scroll handles it).
  $: pageStep = computePageStep(effectiveLayout);

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

  // Arrow keys are direction-aware. RTL manga reads right-to-left,
  // so → advances toward the END of the book (which means a LOWER
  // page number visually but a HIGHER one in archive order — the
  // archive order matches the manga's intended flip order).
  // PageDown / Space always advance regardless of direction (those
  // keys are about reading flow, not spatial direction).
  // The direction/layout/key → delta math lives in ./reader-nav so
  // it can be unit-tested without mounting the page.
  function onKey(e: KeyboardEvent) {
    const abs = keyToAbsolutePage(e.key, pageCount, direction, layout);
    if (abs !== null) {
      e.preventDefault();
      go(abs);
      return;
    }
    const delta = keyToPageDelta(e.key, direction, layout);
    if (delta !== null) {
      e.preventDefault();
      go(pageNum + delta);
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
        {#if !isEpub}
          <!-- Reader-mode controls. Hidden on EPUB (epub.js owns
               its own pagination). Direction toggle covers manga
               (rtl) + webtoon (ttb); layout toggle adds spread for
               two-page comics / manga. ttb forces scroll layout
               regardless of the layout pick. -->
          <div class="mode-controls" aria-label="Reader mode">
            <select class="mode-select"
                    aria-label="Reading direction"
                    bind:value={direction}>
              <option value="ltr">Left → Right</option>
              <option value="rtl">Right → Left</option>
              <option value="ttb">Vertical (webtoon)</option>
            </select>
            {#if direction !== 'ttb'}
              <select class="mode-select"
                      aria-label="Page layout"
                      bind:value={layout}>
                <option value="single">Single page</option>
                <option value="spread">Two-page spread</option>
                <option value="scroll">Long scroll</option>
              </select>
            {/if}
          </div>
        {/if}
        {#if isAdmin}
          <button class="btn-remove" on:click={removeItem}
                  title="Soft-delete this book">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="13" height="13"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
            Remove
          </button>
        {/if}
      </div>
    </header>

    {#if effectiveLayout === 'scroll' && !isEpub}
      <!-- Webtoon / long-scroll mode: stack every page in a single
           column and let the browser handle scroll. Range slider in
           the bottombar still works (it scrolls the matching image
           into view). -->
      <div class="reader-scroll">
        {#each Array(pageCount) as _, i}
          <img class="page-img scroll-page"
               src={pageURL(i + 1)}
               alt="Page {i + 1}"
               loading="lazy"
               id="scroll-page-{i + 1}" />
        {/each}
      </div>
    {:else}
      <div class="reader" class:rtl={direction === 'rtl'}>
        <button
          class="nav-btn left"
          on:click={() => go(direction === 'rtl' ? pageNum + pageStep : pageNum - pageStep)}
          disabled={!isEpub && (direction === 'rtl' ? pageNum + pageStep > pageCount : pageNum <= 1)}
          aria-label={direction === 'rtl' ? 'Next page' : 'Previous page'}
        >‹</button>

        <div class="page-frame" class:spread={effectiveLayout === 'spread' && !isEpub}>
          {#if isEpub}
            <div class="epub-viewer" bind:this={epubViewerEl}></div>
          {:else if effectiveLayout === 'spread'}
            <!-- Two-page spread: render pages [n, n+1] side-by-side.
                 Direction governs visual order — RTL manga shows
                 the higher page number on the LEFT (the way print
                 manga lay out across two facing pages). -->
            {#key pageNum}
              {#if direction === 'rtl'}
                {#if pageNum + 1 <= pageCount}
                  <img class="page-img" src={pageURL(pageNum + 1)} alt="Page {pageNum + 1}" />
                {/if}
                <img class="page-img" src={pageURL(pageNum)} alt="Page {pageNum}" />
              {:else}
                <img class="page-img" src={pageURL(pageNum)} alt="Page {pageNum}" />
                {#if pageNum + 1 <= pageCount}
                  <img class="page-img" src={pageURL(pageNum + 1)} alt="Page {pageNum + 1}" />
                {/if}
              {/if}
            {/key}
          {:else}
            {#key pageNum}
              <img class="page-img" src={pageURL(pageNum)} alt="Page {pageNum}" />
            {/key}
          {/if}
        </div>

        <button
          class="nav-btn right"
          on:click={() => go(direction === 'rtl' ? pageNum - pageStep : pageNum + pageStep)}
          disabled={!isEpub && (direction === 'rtl' ? pageNum <= 1 : pageNum + pageStep > pageCount)}
          aria-label={direction === 'rtl' ? 'Previous page' : 'Next page'}
        >›</button>
      </div>
    {/if}

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
  /* Two-page spread: split the frame in half so each page gets
     half the width but the full height. Larger than single-page
     mode visually because two narrow pages tile the whole frame. */
  .page-frame.spread {
    flex-direction: row;
    gap: 4px;
  }
  .page-frame.spread .page-img {
    max-width: 50%;
  }
  /* Webtoon / scroll mode: vertical column, full-width images. */
  .reader-scroll {
    flex: 1;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    align-items: center;
    background: var(--bg-elevated);
    border-radius: 6px;
    padding: 0 0.5rem;
  }
  .scroll-page {
    width: 100%;
    max-width: 800px; /* webtoons render best at ~800px wide */
    max-height: none;
    height: auto;
    margin-bottom: 2px;
  }

  /* Mode controls inline with the page counter. Compact selects so
     they don't dominate the topbar. */
  .mode-controls {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
  }
  .mode-select {
    background: var(--input-bg);
    color: var(--text-primary);
    border: 1px solid var(--border);
    border-radius: 6px;
    font-size: 0.72rem;
    padding: 0.2rem 0.4rem;
    cursor: pointer;
  }
  .mode-select:focus { outline: 1px solid var(--accent); outline-offset: 1px; }
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
