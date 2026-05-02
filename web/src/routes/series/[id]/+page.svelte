<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemApi, assetUrl, type ItemDetail, type ChildItem } from '$lib/api';

  let series: ItemDetail | null = null;
  let books: ChildItem[] = [];
  let author: { id: string; title: string } | null = null;
  let loading = true;
  let error = '';

  $: id = $page.params.id!;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    await load();
  });

  $: if (id && series && id !== series.id) {
    load();
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const detail = await itemApi.get(id);
      if (detail.type !== 'book_series') {
        if (detail.type === 'book_author') {
          goto(`/authors/${detail.id}`, { replaceState: true });
          return;
        }
        if (detail.type === 'audiobook') {
          goto(`/audiobooks/${detail.id}`, { replaceState: true });
          return;
        }
        goto(`/libraries/${detail.library_id}`, { replaceState: true });
        return;
      }
      series = detail;

      // Author breadcrumb — best-effort, the series row's parent is
      // the author row. A series with a missing parent is an
      // anomaly (scanner should always create both); silently fall
      // through to "Audiobooks" as the breadcrumb root.
      author = null;
      if (detail.parent_id) {
        try {
          const a = await itemApi.get(detail.parent_id);
          author = { id: a.id, title: a.title };
        } catch {
          // Non-fatal.
        }
      }

      const list = await itemApi.children(id);
      // Series order: by year first (release order is usually
      // reading order), then title for siblings within a year.
      // The scanner doesn't yet emit a `series_index` so year is
      // the best signal we have on the server side.
      books = list.items
        .filter(c => c.type === 'audiobook')
        .sort((a, b) => {
          const ya = a.year ?? 0;
          const yb = b.year ?? 0;
          if (ya !== yb) return ya - yb;
          return a.title.localeCompare(b.title);
        });
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load series';
    } finally {
      loading = false;
    }
  }
</script>

<svelte:head><title>{series?.title ?? 'Series'} — OnScreen</title></svelte:head>

<div class="page">
  {#if loading}
    <p class="loading">Loading…</p>
  {:else if error}
    <p class="err">{error}</p>
  {:else if series}
    <nav class="crumb">
      <a href="/">Libraries</a>
      <span>/</span>
      <a href="/libraries/{series.library_id}">Audiobooks</a>
      {#if author}
        <span>/</span>
        <a href="/authors/{author.id}">{author.title}</a>
      {/if}
      <span>/</span>
      <span>{series.title}</span>
    </nav>

    <header class="hero">
      {#if series.poster_path}
        <img class="hero-poster"
             src={assetUrl(`/artwork/${encodeURI(series.poster_path)}?v=${series.updated_at}&w=400`)}
             alt={series.title} />
      {:else}
        <div class="hero-poster placeholder">📚</div>
      {/if}
      <div class="hero-meta">
        <div class="kind">Series</div>
        <h1>{series.title}</h1>
        {#if author}
          <div class="counts">by <a class="author-link" href="/authors/{author.id}">{author.title}</a></div>
        {/if}
        <div class="counts">
          {books.length} {books.length === 1 ? 'book' : 'books'}
        </div>
        {#if series.summary}
          <p class="bio">{series.summary}</p>
        {/if}
      </div>
    </header>

    {#if books.length === 0}
      <p class="empty">No books in this series yet.</p>
    {:else}
      <div class="grid">
        {#each books as b (b.id)}
          <a class="card" href="/audiobooks/{b.id}">
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
    width: 200px; height: 300px; object-fit: cover; border-radius: 6px;
    background: var(--surface); box-shadow: 0 8px 24px rgba(0,0,0,0.4);
  }
  .hero-poster.placeholder {
    display: flex; align-items: center; justify-content: center;
    font-size: 4rem; color: var(--text-muted);
  }
  .hero-meta { flex: 1; min-width: 0; }
  .kind { text-transform: uppercase; font-size: 0.7rem; letter-spacing: 0.1em; color: var(--accent); margin-bottom: 0.5rem; }
  .hero-meta h1 { font-size: 2.5rem; margin: 0 0 0.5rem; }
  .counts { color: var(--text-muted); font-size: 0.9rem; margin-bottom: 0.5rem; }
  .author-link { color: var(--text-secondary); text-decoration: none; }
  .author-link:hover { color: var(--accent); }
  .bio { color: var(--text-secondary); line-height: 1.5; max-width: 70ch; margin-top: 0.5rem; }

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
    .hero-poster { width: 140px; height: 210px; }
    .hero-meta h1 { font-size: 1.6rem; }
  }
</style>
