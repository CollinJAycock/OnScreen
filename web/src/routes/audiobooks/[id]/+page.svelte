<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemApi, assetUrl, type ItemDetail, type ChildItem } from '$lib/api';
  import { audio, currentTrack, type AudioTrack } from '$lib/stores/audio';

  // Audiobook detail page: shows the book + lists its chapters (when the
  // book is multi-file, each audiobook_chapter is its own row with its
  // own file). Single-file books with no chapter children get just a
  // "Play" button — the watch page handles their embedded chapter
  // markers separately for the resume-snap feature, but here they
  // surface as one tile.
  //
  // Mirrors albums/[id]/+page.svelte one-for-one because the data shape
  // is identical (audiobook → audiobook_chapter ≅ album → track). Kept
  // separate so navigation crumbs ("Audiobooks" / author / series) and
  // playback metadata (book title, author byline) stay native to books.

  let book: ItemDetail | null = null;
  let chapters: ChildItem[] = [];
  let chapterDetails: Map<string, ItemDetail> = new Map();
  let bookFile: { id: string } | null = null; // single-file books store their stream here
  let author: { id: string; title: string } | null = null;
  let series: { id: string; title: string } | null = null;
  let loading = true;
  let error = '';
  let isAdmin = false;

  $: id = $page.params.id!;
  $: nowPlayingId = $currentTrack?.id ?? null;

  onMount(async () => {
    const raw = localStorage.getItem('onscreen_user');
    if (!raw) { goto('/login'); return; }
    try { isAdmin = !!JSON.parse(raw)?.is_admin; } catch { /* keep false */ }
    await load();
  });

  async function removeItem() {
    if (!book) return;
    const confirmed = confirm(
      `Soft-delete "${book.title}" and all its chapters?\n\n` +
      `This hides the book from the library. The on-disk files are not touched. ` +
      `Use this to clear ghost rows from misorganised content.`
    );
    if (!confirmed) return;
    try {
      await itemApi.remove(book.id);
      if (series) goto(`/series/${series.id}`);
      else if (author) goto(`/authors/${author.id}`);
      else goto(`/libraries/${book.library_id}`);
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Remove failed');
    }
  }

  $: if (id && book && id !== book.id) {
    load();
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const detail = await itemApi.get(id);
      if (detail.type !== 'audiobook') {
        // Wrong type for this route — bounce to where the item lives.
        if (detail.type === 'book_author') {
          goto(`/authors/${detail.id}`, { replaceState: true });
          return;
        }
        if (detail.type === 'book_series') {
          goto(`/series/${detail.id}`, { replaceState: true });
          return;
        }
        goto(`/libraries/${detail.library_id}`, { replaceState: true });
        return;
      }
      book = detail;
      bookFile = detail.files[0] ? { id: detail.files[0].id } : null;

      // Walk the parent chain for the breadcrumb. parent may be either
      // a book_series (parent.parent = book_author) or directly a
      // book_author (standalone book under an author with no series).
      author = null;
      series = null;
      if (detail.parent_id) {
        try {
          const parent = await itemApi.get(detail.parent_id);
          if (parent.type === 'book_series') {
            series = { id: parent.id, title: parent.title };
            if (parent.parent_id) {
              try {
                const grand = await itemApi.get(parent.parent_id);
                if (grand.type === 'book_author') {
                  author = { id: grand.id, title: grand.title };
                }
              } catch {
                // Orphaned series — skip the author breadcrumb.
              }
            }
          } else if (parent.type === 'book_author') {
            author = { id: parent.id, title: parent.title };
          }
        } catch {
          // Non-fatal: orphan book just renders without breadcrumb.
        }
      }

      const list = await itemApi.children(id);
      chapters = list.items
        .filter((c) => c.type === 'audiobook_chapter')
        .sort((a, b) => (a.index ?? 9999) - (b.index ?? 9999));

      // Resolve full detail for every chapter in parallel — needed for
      // the file id (the audio store streams by file_id, not item_id).
      const map = new Map<string, ItemDetail>();
      await Promise.all(
        chapters.map(async (c) => {
          try {
            const cd = await itemApi.get(c.id);
            if (cd.files.length > 0) map.set(c.id, cd);
          } catch {
            // Chapter row with no file — disabled in the UI.
          }
        }),
      );
      chapterDetails = map;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load book';
    } finally {
      loading = false;
    }
  }

  function buildQueue(startIdx: number): { queue: AudioTrack[]; index: number } {
    const queue: AudioTrack[] = [];
    let index = 0;
    for (let i = 0; i < chapters.length; i++) {
      const c = chapters[i];
      const cd = chapterDetails.get(c.id);
      const fileId = cd?.files[0]?.id;
      if (!fileId) continue;
      if (i === startIdx) index = queue.length;
      queue.push({
        id: c.id,
        fileId,
        title: c.title,
        durationMS: c.duration_ms,
        index: c.index,
        album: book?.title,        // re-purposed: "now playing — Foo" header shows book title
        albumId: book?.id,
        artist: author?.title,     // narrator/author byline
        artistId: author?.id,
        posterPath: book?.poster_path,
      });
    }
    return { queue, index };
  }

  function playChapter(idx: number) {
    const c = chapters[idx];
    if (!chapterDetails.has(c.id)) return;
    const { queue, index } = buildQueue(idx);
    audio.play(queue, index);
  }

  function playBook() {
    // Multi-file: start from chapter 0. Single-file: route to the
    // watch page where the embedded chapter-marker resume-snap and
    // full HLS pipeline live (audio store doesn't yet handle the
    // single-file-with-chapter-markers case).
    if (chapters.length > 0) {
      const firstPlayable = chapters.findIndex((c) => chapterDetails.has(c.id));
      if (firstPlayable >= 0) playChapter(firstPlayable);
      return;
    }
    if (book) goto(`/watch/${book.id}`);
  }

  function formatDuration(ms?: number): string {
    if (!ms) return '';
    const s = Math.round(ms / 1000);
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`;
    return `${m}:${String(s % 60).padStart(2, '0')}`;
  }

  function totalDuration(): string {
    const ms = chapters.length > 0
      ? chapters.reduce((sum, c) => sum + (c.duration_ms ?? 0), 0)
      : (book?.duration_ms ?? 0);
    if (!ms) return '';
    const min = Math.floor(ms / 60000);
    if (min < 60) return `${min} min`;
    return `${Math.floor(min / 60)}h ${min % 60}m`;
  }

  $: chapterCountLabel =
    chapters.length === 1 ? '1 chapter' : `${chapters.length} chapters`;
</script>

<svelte:head><title>{book?.title ?? 'Audiobook'} — OnScreen</title></svelte:head>

<div class="page">
  {#if loading}
    <p class="loading">Loading…</p>
  {:else if error}
    <p class="err">{error}</p>
  {:else if book}
    <nav class="crumb">
      <a href="/">Libraries</a>
      <span>/</span>
      <a href="/libraries/{book.library_id}">Audiobooks</a>
      {#if author}
        <span>/</span>
        <a href="/authors/{author.id}">{author.title}</a>
      {/if}
      {#if series}
        <span>/</span>
        <a href="/series/{series.id}">{series.title}</a>
      {/if}
      <span>/</span>
      <span>{book.title}</span>
    </nav>

    <header class="hero">
      {#if book.poster_path}
        <img class="hero-poster"
             src={assetUrl(`/artwork/${encodeURI(book.poster_path)}?v=${book.updated_at}&w=400`)}
             alt={book.title} />
      {:else}
        <div class="hero-poster placeholder">🎧</div>
      {/if}
      <div class="hero-meta">
        <div class="kind">Audiobook</div>
        <h1>{book.title}</h1>
        {#if author}
          <div class="byline">by <a href="/authors/{author.id}">{author.title}</a></div>
        {:else if book.original_title}
          <div class="byline">by {book.original_title}</div>
        {/if}
        <div class="counts">
          {#if book.year}{book.year} · {/if}
          {#if chapters.length > 0}{chapterCountLabel}{:else}1 file{/if}
          {#if totalDuration()} · {totalDuration()}{/if}
        </div>
        <div class="actions">
          <button class="btn-play" on:click={playBook}
                  disabled={chapters.length === 0 ? !bookFile : chapterDetails.size === 0}>
            <span class="ico">▶</span> Play
          </button>
          {#if isAdmin}
            <button class="btn-remove" on:click={removeItem}
                    title="Soft-delete this book and its chapters">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
              Remove
            </button>
          {/if}
        </div>
        {#if book.summary}<p class="bio">{book.summary}</p>{/if}
      </div>
    </header>

    {#if chapters.length === 0}
      <p class="empty">Single-file audiobook — press Play to start.</p>
    {:else}
      <ol class="tracks">
        {#each chapters as c, i (c.id)}
          {@const detail = chapterDetails.get(c.id)}
          {@const playable = !!detail}
          {@const playing = nowPlayingId === c.id}
          <li class="row" class:playing class:disabled={!playable}>
            <button class="num" on:click={() => playChapter(i)} disabled={!playable}
                    title={playable ? `Play ${c.title}` : 'No file available'}>
              {#if playing}
                <span class="eq" aria-hidden="true">♫</span>
              {:else}
                <span class="num-text">{c.index ?? i + 1}</span>
                <span class="num-play" aria-hidden="true">▶</span>
              {/if}
            </button>
            <div class="title">{c.title}</div>
            <div class="dur">{formatDuration(c.duration_ms)}</div>
          </li>
        {/each}
      </ol>
    {/if}
  {/if}
</div>

<style>
  .page { padding: 2.5rem 2.5rem 5rem; max-width: 1200px; margin: 0 auto; }

  .crumb {
    display: flex; align-items: center; gap: 0.4rem;
    font-size: 0.75rem; color: var(--text-muted); margin-bottom: 1.5rem;
    flex-wrap: wrap;
  }
  .crumb a { color: var(--text-muted); text-decoration: none; }
  .crumb a:hover { color: var(--text-secondary); }

  .hero { display: flex; gap: 2rem; margin-bottom: 2.5rem; align-items: flex-end; }
  .hero-poster {
    width: 220px; height: 220px; object-fit: cover; border-radius: 8px;
    background: var(--surface); box-shadow: 0 8px 24px rgba(0,0,0,0.4);
  }
  .hero-poster.placeholder {
    display: flex; align-items: center; justify-content: center;
    font-size: 5rem; color: var(--text-muted);
  }
  .hero-meta { flex: 1; min-width: 0; }
  .kind { text-transform: uppercase; font-size: 0.7rem; letter-spacing: 0.1em; color: var(--accent); margin-bottom: 0.5rem; }
  .hero-meta h1 { font-size: 2.5rem; margin: 0 0 0.4rem; line-height: 1.1; }
  .byline { color: var(--text-secondary); margin-bottom: 0.4rem; }
  .byline a { color: var(--text-secondary); text-decoration: none; }
  .byline a:hover { color: var(--accent); }
  .counts { color: var(--text-muted); font-size: 0.85rem; margin-bottom: 1rem; }
  .bio { color: var(--text-secondary); line-height: 1.5; max-width: 70ch; }

  .actions { margin-bottom: 1rem; display: flex; gap: 0.6rem; align-items: center; flex-wrap: wrap; }
  .btn-play {
    display: inline-flex; align-items: center; gap: 0.5rem;
    background: var(--accent); color: white; border: 0; padding: 0.6rem 1.4rem;
    border-radius: 999px; font-size: 0.9rem; font-weight: 600; cursor: pointer;
  }
  .btn-play:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-play:hover:not(:disabled) { filter: brightness(1.1); }
  .btn-play .ico { font-size: 0.7rem; }

  .btn-remove {
    display: inline-flex; align-items: center; gap: 0.35rem;
    background: var(--input-bg, transparent);
    border: 1px solid rgba(204,102,102,0.3);
    border-radius: 6px;
    color: #c66; font-size: 0.78rem; font-weight: 500;
    cursor: pointer; padding: 0.45rem 0.9rem;
    transition: all 0.12s;
  }
  .btn-remove:hover { color: #e88; border-color: rgba(232,136,136,0.5); background: var(--bg-hover, rgba(204,102,102,0.06)); }

  .tracks { list-style: none; padding: 0; margin: 0;
            border-top: 1px solid var(--border, rgba(255,255,255,0.08)); }
  .row {
    display: grid; grid-template-columns: 3rem 1fr auto;
    gap: 0.75rem; align-items: center;
    padding: 0.5rem 0.75rem;
    border-bottom: 1px solid var(--border, rgba(255,255,255,0.06));
    font-size: 0.95rem;
  }
  .row:hover:not(.disabled) { background: var(--surface-hover, rgba(255,255,255,0.04)); }
  .row.playing { color: var(--accent); }
  .row.disabled { opacity: 0.4; }

  .num {
    width: 2.5rem; height: 2.5rem; display: inline-flex; align-items: center; justify-content: center;
    background: transparent; border: 0; color: inherit; cursor: pointer;
    border-radius: 4px;
  }
  .num:disabled { cursor: not-allowed; }
  .num-text { color: var(--text-muted); }
  .num-play { display: none; color: var(--accent); }
  .row:hover .num-text { display: none; }
  .row:hover .num-play { display: inline; }
  .row.disabled:hover .num-text { display: inline; }
  .row.disabled:hover .num-play { display: none; }
  .eq { color: var(--accent); }

  .title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .dur { color: var(--text-muted); font-variant-numeric: tabular-nums; font-size: 0.85rem; }

  .empty, .loading, .err { color: var(--text-muted); padding: 2rem 0; }
  .err { color: var(--danger, #f87171); }

  @media (max-width: 600px) {
    .page { padding: 1.5rem 1rem 6rem; }
    .hero { flex-direction: column; align-items: flex-start; gap: 1rem; }
    .hero-poster { width: 160px; height: 160px; }
    .hero-meta h1 { font-size: 1.6rem; }
  }
</style>
