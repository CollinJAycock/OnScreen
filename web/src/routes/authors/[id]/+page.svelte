<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemApi, assetUrl, type ItemDetail, type ChildItem } from '$lib/api';

  let author: ItemDetail | null = null;
  let series: ChildItem[] = [];
  let standaloneBooks: ChildItem[] = [];
  let loading = true;
  let error = '';

  $: id = $page.params.id!;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    await load();
  });

  $: if (id && author && id !== author.id) {
    load();
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const detail = await itemApi.get(id);
      if (detail.type !== 'book_author') {
        // Wrong type for this route — redirect to wherever the item
        // actually lives so a stale URL doesn't end up as a 404.
        if (detail.type === 'book_series') {
          goto(`/series/${detail.id}`, { replaceState: true });
          return;
        }
        if (detail.type === 'audiobook') {
          goto(`/watch/${detail.id}`, { replaceState: true });
          return;
        }
        goto(`/libraries/${detail.library_id}`, { replaceState: true });
        return;
      }
      author = detail;

      // Children of an author are book_series rows + standalone
      // audiobook rows (books not part of a series). Render them
      // as separate sections so the user sees the structure rather
      // than an arbitrary mixed grid.
      const list = await itemApi.children(id);
      series = list.items
        .filter(c => c.type === 'book_series')
        .sort((a, b) => a.title.localeCompare(b.title));
      standaloneBooks = list.items
        .filter(c => c.type === 'audiobook')
        .sort((a, b) => {
          const ya = a.year ?? 0;
          const yb = b.year ?? 0;
          if (ya !== yb) return yb - ya;
          return a.title.localeCompare(b.title);
        });
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load author';
    } finally {
      loading = false;
    }
  }
</script>

<svelte:head><title>{author?.title ?? 'Author'} — OnScreen</title></svelte:head>

<div class="page">
  {#if loading}
    <p class="loading">Loading…</p>
  {:else if error}
    <p class="err">{error}</p>
  {:else if author}
    <nav class="crumb">
      <a href="/">Libraries</a>
      <span>/</span>
      <a href="/libraries/{author.library_id}">Audiobooks</a>
      <span>/</span>
      <span>{author.title}</span>
    </nav>

    <header class="hero">
      {#if author.poster_path}
        <img class="hero-poster"
             src={assetUrl(`/artwork/${encodeURI(author.poster_path)}?v=${author.updated_at}&w=400`)}
             alt={author.title} />
      {:else}
        <div class="hero-poster placeholder">{author.title.charAt(0)}</div>
      {/if}
      <div class="hero-meta">
        <div class="kind">Author</div>
        <h1>{author.title}</h1>
        <div class="counts">
          {#if series.length > 0}
            {series.length} {series.length === 1 ? 'series' : 'series'}
          {/if}
          {#if series.length > 0 && standaloneBooks.length > 0} · {/if}
          {#if standaloneBooks.length > 0}
            {standaloneBooks.length} {standaloneBooks.length === 1 ? 'book' : 'books'}
          {/if}
        </div>
        {#if author.summary}
          <p class="bio">{author.summary}</p>
        {/if}
      </div>
    </header>

    {#if series.length === 0 && standaloneBooks.length === 0}
      <p class="empty">No books in your library yet.</p>
    {/if}

    {#if series.length > 0}
      <h2 class="section-h">Series</h2>
      <div class="grid">
        {#each series as s (s.id)}
          <a class="card" href="/series/{s.id}">
            <div class="poster">
              {#if s.poster_path}
                <img src={assetUrl(`/artwork/${encodeURI(s.poster_path)}?v=${s.updated_at}&w=300`)}
                     alt={s.title} loading="lazy" />
              {:else}
                <div class="poster-blank">📚</div>
              {/if}
            </div>
            <div class="title">{s.title}</div>
          </a>
        {/each}
      </div>
    {/if}

    {#if standaloneBooks.length > 0}
      <h2 class="section-h books-h">Books</h2>
      <div class="grid">
        {#each standaloneBooks as b (b.id)}
          <a class="card" href="/watch/{b.id}">
            <div class="poster">
              {#if b.poster_path}
                <img src={assetUrl(`/artwork/${encodeURI(b.poster_path)}?v=${b.updated_at}&w=300`)}
                     alt={b.title} loading="lazy" />
              {:else}
                <div class="poster-blank">🎧</div>
              {/if}
            </div>
            <div class="title">{b.title}</div>
            {#if b.year}<div class="year">{b.year}</div>{/if}
          </a>
        {/each}
      </div>
    {/if}
  {/if}
</div>

<style>
  .page { padding: 2.5rem 2.5rem 5rem; max-width: 1400px; margin: 0 auto; }

  .crumb {
    display: flex; align-items: center; gap: 0.4rem;
    font-size: 0.75rem; color: var(--text-muted); margin-bottom: 1.5rem;
  }
  .crumb a { color: var(--text-muted); text-decoration: none; }
  .crumb a:hover { color: var(--text-secondary); }

  .hero { display: flex; gap: 2rem; margin-bottom: 2.5rem; align-items: flex-end; }
  .hero-poster {
    width: 200px; height: 200px; object-fit: cover; border-radius: 50%;
    background: var(--surface); box-shadow: 0 8px 24px rgba(0,0,0,0.4);
  }
  .hero-poster.placeholder {
    display: flex; align-items: center; justify-content: center;
    font-size: 4rem; color: var(--text-muted); font-weight: 200;
  }
  .hero-meta { flex: 1; min-width: 0; }
  .kind { text-transform: uppercase; font-size: 0.7rem; letter-spacing: 0.1em; color: var(--accent); margin-bottom: 0.5rem; }
  .hero-meta h1 { font-size: 2.5rem; margin: 0 0 0.5rem; }
  .counts { color: var(--text-muted); font-size: 0.9rem; margin-bottom: 1rem; }
  .bio { color: var(--text-secondary); line-height: 1.5; max-width: 70ch; }

  .section-h { font-size: 1.1rem; margin: 0 0 1rem; }
  .books-h { margin-top: 2.5rem; }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 1.5rem 1rem;
  }

  .card { text-decoration: none; color: inherit; display: block; }
  .poster {
    aspect-ratio: 2 / 3; border-radius: 6px; overflow: hidden;
    background: var(--surface); margin-bottom: 0.5rem;
    box-shadow: 0 4px 12px rgba(0,0,0,0.3);
    transition: transform 0.15s ease, box-shadow 0.15s ease;
  }
  .card:hover .poster { transform: translateY(-2px); box-shadow: 0 8px 18px rgba(0,0,0,0.5); }
  .poster img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .poster-blank {
    width: 100%; height: 100%; display: flex; align-items: center; justify-content: center;
    color: var(--text-muted); font-size: 2rem;
  }
  .title { font-size: 0.9rem; line-height: 1.3; overflow: hidden; text-overflow: ellipsis;
           display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
  .year { font-size: 0.75rem; color: var(--text-muted); margin-top: 0.15rem; }

  .empty, .loading, .err { color: var(--text-muted); padding: 2rem 0; }
  .err { color: var(--danger, #f87171); }

  @media (max-width: 600px) {
    .page { padding: 1.5rem 1rem 4rem; }
    .hero { flex-direction: column; align-items: flex-start; gap: 1rem; }
    .hero-poster { width: 140px; height: 140px; }
    .hero-meta h1 { font-size: 1.6rem; }
  }
</style>
