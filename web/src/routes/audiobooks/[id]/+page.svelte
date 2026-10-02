<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemApi, assetUrl, type ItemDetail, type ChildItem, type Bookmark } from '$lib/api';
  import { confirmAction } from '$lib/native';
  import { audio, currentTrack, type AudioTrack } from '$lib/stores/audio';
  import { bookmarkAdded } from '$lib/stores/bookmarks';
  import { toast } from '$lib/stores/toast';
  import { replayGainFromFile } from '$lib/replaygain';
  import { chapterAt, formatPosition, insertBookmark, listeningUnavailable, resumeStartMS } from '$lib/audiobook';

  // Audiobook detail page: shows the book + lists its chapters (when the
  // book is multi-file, each audiobook_chapter is its own row with its
  // own file). A single-file book lists its embedded chapters instead
  // (files[0].chapters), and plays as one track in the global audio
  // player, which skips between those chapters and resumes at the start
  // of the one you were in. Below the chapters: the listener's bookmarks.
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

  // The listener's bookmarks in this book, in listening order. The section
  // hides when the server has no bookmark routes (an older server).
  let bookmarks: Bookmark[] = [];
  let bookmarksLoaded = false;
  let bookmarksAvailable = true;
  let bookmarksError = '';
  let editingId = '';
  let editNote = '';
  let savingNote = false;

  $: id = $page.params.id!;
  $: nowPlayingId = $currentTrack?.id ?? null;
  // A single-file book's embedded chapters, and the one playing now.
  $: bookChapters = book?.files[0]?.chapters ?? [];
  $: playingChapter = book && nowPlayingId === book.id ? chapterAt(bookChapters, $audio.positionMS) : null;

  onMount(async () => {
    const raw = localStorage.getItem('onscreen_user');
    if (!raw) { goto('/login'); return; }
    try { isAdmin = !!JSON.parse(raw)?.is_admin; } catch { /* keep false */ }
    await load();
  });

  // A bookmark added from the player while this page is open joins the
  // list in its place.
  onMount(() =>
    bookmarkAdded.subscribe((evt) => {
      if (evt && book && evt.bookId === book.id) bookmarks = insertBookmark(bookmarks, evt.bookmark, book.id);
    })
  );

  async function removeItem() {
    if (!book) return;
    const confirmed = await confirmAction(
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
      toast.error(e instanceof Error ? e.message : 'Remove failed');
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
      void loadBookmarks(detail.id);

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
        replayGain: replayGainFromFile(cd?.files[0]),
        audiobook: book ? { bookId: book.id } : undefined,
      });
    }
    return { queue, index };
  }

  function playChapter(idx: number, startMS = 0) {
    const c = chapters[idx];
    if (!chapterDetails.has(c.id)) return;
    const { queue, index } = buildQueue(idx);
    audio.play(queue, index, startMS);
  }

  // A single-file book is one track; its embedded chapters ride along for
  // the player's chapter skips and "end of chapter" sleep timer.
  function bookTrack(): AudioTrack | null {
    const f = book?.files[0];
    if (!book || !f) return null;
    return {
      id: book.id,
      fileId: f.id,
      title: book.title,
      durationMS: book.duration_ms ?? f.duration_ms,
      artist: author?.title,     // author byline
      artistId: author?.id,
      posterPath: book.poster_path,
      replayGain: replayGainFromFile(f),
      audiobook: { bookId: book.id, chapters: f.chapters ?? [] },
    };
  }

  // Play itemId (the book, or one of its chapters) from startMS — a
  // bookmark, or an embedded chapter. When the player already has that
  // item, just move there: queueing the same track again wouldn't seek.
  function playFrom(itemId: string, startMS: number) {
    if (nowPlayingId === itemId) {
      audio.seek(startMS);
      audio.resume();
      return;
    }
    if (book && itemId === book.id) {
      const t = bookTrack();
      if (t) audio.play([t], 0, startMS);
      return;
    }
    const idx = chapters.findIndex((c) => c.id === itemId);
    if (idx >= 0 && chapterDetails.has(itemId)) playChapter(idx, startMS);
    else toast.error("That chapter isn't available to play");
  }

  // findResumePoint scans chapters back-to-front looking for the most
  // recently-progressed one. The latest chapter with view_offset_ms > 0
  // wins — earlier chapters are presumed finished. Returns null when
  // no chapter has progress (fresh book).
  function findResumePoint(): { chapterIdx: number; positionMS: number } | null {
    for (let i = chapters.length - 1; i >= 0; i--) {
      const cd = chapterDetails.get(chapters[i].id);
      if (cd && cd.view_offset_ms > 0) {
        return { chapterIdx: i, positionMS: cd.view_offset_ms };
      }
    }
    return null;
  }

  $: resumePoint = chapterDetails.size > 0 ? findResumePoint() : null;

  // Single-file resume: the start of the embedded chapter the saved
  // offset is in (0 = nothing to resume).
  $: singleResumeMS = book && chapters.length === 0
    ? resumeStartMS(bookChapters, book.view_offset_ms, book.duration_ms ?? 0)
    : 0;
  $: singleResumeChapter = singleResumeMS > 0 ? chapterAt(bookChapters, singleResumeMS) : null;

  function playBook() {
    // Multi-file: start from the resume point if any, otherwise the
    // first playable chapter. Single-file: the one track, from its
    // resume point.
    if (chapters.length > 0) {
      if (resumePoint) {
        playChapter(resumePoint.chapterIdx, resumePoint.positionMS);
        return;
      }
      const firstPlayable = chapters.findIndex((c) => chapterDetails.has(c.id));
      if (firstPlayable >= 0) playChapter(firstPlayable);
      return;
    }
    const t = bookTrack();
    if (t) audio.play([t], 0, singleResumeMS);
  }

  function startFromBeginning() {
    if (chapters.length > 0) {
      const firstPlayable = chapters.findIndex((c) => chapterDetails.has(c.id));
      if (firstPlayable >= 0) playChapter(firstPlayable);
    } else if (book) {
      playFrom(book.id, 0);
    }
  }

  async function loadBookmarks(bookId: string) {
    bookmarks = [];
    bookmarksLoaded = false;
    bookmarksAvailable = true;
    bookmarksError = '';
    cancelEdit();
    try {
      const list = await itemApi.listBookmarks(bookId);
      if (book?.id !== bookId) return;
      bookmarks = list ?? [];
      bookmarksLoaded = true;
    } catch (e) {
      if (book?.id !== bookId) return;
      if (listeningUnavailable(e)) bookmarksAvailable = false;
      else bookmarksError = "Couldn't load bookmarks.";
    }
  }

  // The chapter a bookmark sits in: the chapter file for a multi-file
  // book, the embedded chapter at its position for a single-file one.
  function bookmarkChapter(b: Bookmark): string {
    if (book && b.item_id !== book.id) return b.item_title;
    return chapterAt(bookChapters, b.position_ms)?.title ?? '';
  }

  function startEdit(b: Bookmark) {
    editingId = b.id;
    editNote = b.note;
  }

  function cancelEdit() {
    editingId = '';
    editNote = '';
  }

  async function saveNote(b: Bookmark) {
    if (savingNote) return;
    const note = editNote.trim();
    savingNote = true;
    try {
      await itemApi.updateBookmark(b.id, note);
      bookmarks = bookmarks.map((x) => (x.id === b.id ? { ...x, note } : x));
      cancelEdit();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : 'Could not save the note');
    } finally {
      savingNote = false;
    }
  }

  async function removeBookmark(b: Bookmark) {
    try {
      await itemApi.deleteBookmark(b.id);
    } catch (e: unknown) {
      // Already gone (deleted from another device): drop it here too.
      if (!listeningUnavailable(e)) {
        toast.error(e instanceof Error ? e.message : 'Could not delete the bookmark');
        return;
      }
    }
    bookmarks = bookmarks.filter((x) => x.id !== b.id);
    if (editingId === b.id) cancelEdit();
    toast.success('Bookmark deleted');
  }

  function focusOnMount(node: HTMLElement) {
    node.focus();
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
                  disabled={chapters.length === 0 ? !bookFile : chapterDetails.size === 0}
                  title={resumePoint
                    ? `Resume "${chapters[resumePoint.chapterIdx]?.title ?? ''}" at ${formatDuration(resumePoint.positionMS)}`
                    : singleResumeMS > 0
                      ? `Resume at ${formatPosition(singleResumeMS)}`
                      : 'Play from the first chapter'}>
            <span class="ico">▶</span>
            {#if resumePoint}
              Resume
              <span class="resume-meta">
                · ch&nbsp;{(chapters[resumePoint.chapterIdx]?.index ?? resumePoint.chapterIdx + 1)} · {formatDuration(resumePoint.positionMS)}
              </span>
            {:else if singleResumeMS > 0}
              Resume
              <span class="resume-meta">
                {#if singleResumeChapter}· ch&nbsp;{bookChapters.indexOf(singleResumeChapter) + 1}{/if}
                · {formatPosition(singleResumeMS)}
              </span>
            {:else}
              Play
            {/if}
          </button>
          {#if resumePoint || singleResumeMS > 0}
            <button class="btn-restart" on:click={startFromBeginning}
                    title="Start from chapter 1">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="13" height="13"><polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/></svg>
              Start over
            </button>
          {/if}
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
      {#if bookFile && bookChapters.length > 0}
        <ol class="tracks">
          {#each bookChapters as ch, i (i)}
            {@const playing = playingChapter === ch}
            <li class="row" class:playing>
              <button class="num" on:click={() => book && playFrom(book.id, ch.start_ms)}
                      title="Play {ch.title || `chapter ${i + 1}`}">
                {#if playing}
                  <span class="eq" aria-hidden="true">♫</span>
                {:else}
                  <span class="num-text">{i + 1}</span>
                  <span class="num-play" aria-hidden="true">▶</span>
                {/if}
              </button>
              <div class="title">{ch.title || `Chapter ${i + 1}`}</div>
              <div class="dur">{formatDuration(Math.max(0, ch.end_ms - ch.start_ms))}</div>
            </li>
          {/each}
        </ol>
      {:else}
        <p class="empty">Single-file audiobook — press Play to start.</p>
      {/if}
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

    {#if bookmarksAvailable}
      <section class="bookmarks" aria-labelledby="bookmarks-heading">
        <h2 id="bookmarks-heading">Bookmarks</h2>
        {#if bookmarksError}
          <p class="bm-empty">{bookmarksError}</p>
        {:else if bookmarks.length === 0}
          {#if bookmarksLoaded}
            <p class="bm-empty">No bookmarks yet — add one from the player while you listen.</p>
          {/if}
        {:else}
          <ul class="bm-list">
            {#each bookmarks as b (b.id)}
              {@const where = bookmarkChapter(b)}
              <li class="bm-row">
                <button class="bm-play" on:click={() => playFrom(b.item_id, b.position_ms)}
                        title="Play from here"
                        aria-label="Play from {where ? `${where}, ` : ''}{formatPosition(b.position_ms)}">
                  <span class="ico" aria-hidden="true">▶</span>
                  {formatPosition(b.position_ms)}
                </button>
                <div class="bm-body">
                  {#if where}<div class="bm-chapter">{where}</div>{/if}
                  {#if editingId === b.id}
                    <form class="bm-edit" on:submit|preventDefault={() => saveNote(b)}>
                      <input
                        type="text"
                        maxlength="500"
                        placeholder="Note"
                        aria-label="Bookmark note"
                        bind:value={editNote}
                        use:focusOnMount
                        on:keydown={(e) => { if (e.key === 'Escape') cancelEdit(); }}
                      />
                      <button type="submit" class="bm-btn primary" disabled={savingNote}>Save</button>
                      <button type="button" class="bm-btn" on:click={cancelEdit}>Cancel</button>
                    </form>
                  {:else if b.note}
                    <div class="bm-note">{b.note}</div>
                  {/if}
                </div>
                {#if editingId !== b.id}
                  <div class="bm-actions">
                    <button class="bm-btn" on:click={() => startEdit(b)}>{b.note ? 'Edit note' : 'Add note'}</button>
                    <button class="bm-btn danger" on:click={() => removeBookmark(b)}
                            aria-label="Delete bookmark at {formatPosition(b.position_ms)}">Delete</button>
                  </div>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </section>
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
  .resume-meta {
    font-size: 0.78rem; font-weight: 400; opacity: 0.85;
    margin-left: 0.15rem;
  }

  .btn-restart {
    display: inline-flex; align-items: center; gap: 0.35rem;
    background: var(--input-bg, transparent);
    border: 1px solid var(--border-strong, rgba(255,255,255,0.12));
    border-radius: 999px;
    color: var(--text-secondary); font-size: 0.8rem; font-weight: 500;
    cursor: pointer; padding: 0.45rem 0.95rem;
    transition: all 0.12s;
  }
  .btn-restart:hover { color: var(--text-primary); background: var(--bg-hover, rgba(255,255,255,0.05)); }

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

  .bookmarks { margin-top: 2.5rem; }
  .bookmarks h2 { font-size: 1.1rem; margin: 0 0 0.75rem; }
  .bm-empty { color: var(--text-muted); font-size: 0.9rem; margin: 0; }
  .bm-list { list-style: none; padding: 0; margin: 0;
             border-top: 1px solid var(--border, rgba(255,255,255,0.08)); }
  .bm-row {
    display: grid; grid-template-columns: auto 1fr auto;
    gap: 0.75rem; align-items: center;
    padding: 0.55rem 0.75rem;
    border-bottom: 1px solid var(--border, rgba(255,255,255,0.06));
  }
  .bm-row:hover { background: var(--surface-hover, rgba(255,255,255,0.04)); }
  .bm-play {
    display: inline-flex; align-items: center; gap: 0.4rem;
    background: transparent; border: 1px solid var(--border-strong, rgba(255,255,255,0.12));
    border-radius: 999px; color: var(--text-secondary); cursor: pointer;
    padding: 0.3rem 0.8rem; font-size: 0.8rem; font-variant-numeric: tabular-nums;
  }
  .bm-play .ico { font-size: 0.6rem; }
  .bm-play:hover { color: var(--accent); border-color: var(--accent); }
  .bm-body { min-width: 0; }
  .bm-chapter { font-size: 0.9rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .bm-note { color: var(--text-secondary); font-size: 0.85rem; line-height: 1.4;
             white-space: pre-wrap; overflow-wrap: anywhere; }
  .bm-actions { display: flex; gap: 0.4rem; }
  .bm-btn {
    background: var(--input-bg, transparent);
    border: 1px solid var(--border-strong, rgba(255,255,255,0.12)); border-radius: 6px;
    color: var(--text-secondary); font-size: 0.75rem; font-weight: 500;
    cursor: pointer; padding: 0.3rem 0.7rem; transition: all 0.12s;
  }
  .bm-btn:hover { color: var(--text-primary); background: var(--bg-hover, rgba(255,255,255,0.05)); }
  .bm-btn:disabled { opacity: 0.6; cursor: default; }
  .bm-btn.primary { background: var(--accent); border-color: var(--accent); color: white; }
  .bm-btn.danger { color: #c66; border-color: rgba(204,102,102,0.3); }
  .bm-btn.danger:hover { color: #e88; border-color: rgba(232,136,136,0.5); }
  .bm-edit { display: flex; gap: 0.4rem; align-items: center; margin-top: 0.25rem; }
  .bm-edit input {
    flex: 1; min-width: 0; padding: 0.35rem 0.55rem; font-size: 0.85rem;
    background: var(--input-bg, transparent); color: var(--text-primary);
    border: 1px solid var(--border-strong, rgba(255,255,255,0.12)); border-radius: 6px;
  }
  .bm-edit input:focus { outline: none; border-color: var(--accent); }

  @media (max-width: 600px) {
    .page { padding: 1.5rem 1rem 6rem; }
    .hero { flex-direction: column; align-items: flex-start; gap: 1rem; }
    .hero-poster { width: 160px; height: 160px; }
    .hero-meta h1 { font-size: 1.6rem; }
    .bm-row { grid-template-columns: auto 1fr; }
    .bm-actions { grid-column: 1 / -1; justify-content: flex-end; }
  }
</style>
