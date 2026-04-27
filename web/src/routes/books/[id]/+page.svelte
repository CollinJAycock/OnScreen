<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemApi, type ItemDetail } from '$lib/api';

  // v2.1 Stage 1 reader. CBZ-only — pages come from the backend's
  // GET /items/{id}/book/page/{n} endpoint, which streams the n-th
  // sorted image entry from the zip. Bookmarks, full-text search,
  // and reading progress are deferred to a follow-up; the bare
  // minimum here is "open it, flip pages, see the cover."
  //
  // Pagination state lives in the URL query (?p=N) so a refresh /
  // share preserves the current page.

  let book: ItemDetail | null = null;
  let pageNum = 1;
  let pageCount = 0;
  let loading = true;
  let error = '';

  // Preload the next page invisibly so a click feels instant.
  let preload: HTMLImageElement | null = null;

  $: id = $page.params.id!;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    pageNum = parseInt($page.url.searchParams.get('p') ?? '1', 10) || 1;
    await load();
  });

  onDestroy(() => {
    preload = null;
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
      // The scanner stashes page count on duration_ms (re-purposed —
      // see migration 00059's notes). Fall back to 1 so the reader
      // doesn't degenerate when the page count couldn't be probed.
      pageCount = Math.max(1, detail.duration_ms ?? 1);
      pageNum = Math.min(Math.max(pageNum, 1), pageCount);
      schedulePreload();
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load book';
    } finally {
      loading = false;
    }
  }

  function pageURL(n: number): string {
    return `/api/v1/items/${id}/book/page/${n}`;
  }

  function go(n: number) {
    if (n < 1 || n > pageCount) return;
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
      <div class="page-counter">Page {pageNum} of {pageCount}</div>
    </header>

    <div class="reader">
      <button
        class="nav-btn left"
        on:click={() => go(pageNum - 1)}
        disabled={pageNum <= 1}
        aria-label="Previous page"
      >‹</button>

      <div class="page-frame">
        {#key pageNum}
          <img class="page-img" src={pageURL(pageNum)} alt="Page {pageNum}" />
        {/key}
      </div>

      <button
        class="nav-btn right"
        on:click={() => go(pageNum + 1)}
        disabled={pageNum >= pageCount}
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
