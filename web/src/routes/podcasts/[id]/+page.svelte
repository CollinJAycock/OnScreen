<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemApi, assetUrl, type ItemDetail, type ChildItem } from '$lib/api';

  let show: ItemDetail | null = null;
  let episodes: ChildItem[] = [];
  let loading = true;
  let error = '';

  $: id = $page.params.id!;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    await load();
  });

  $: if (id && show && id !== show.id) {
    load();
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const detail = await itemApi.get(id);
      if (detail.type !== 'podcast') {
        // Wrong type for this route — bounce to wherever the item belongs.
        if (detail.type === 'podcast_episode' || detail.type === 'movie' || detail.type === 'episode') {
          goto(`/watch/${detail.id}`, { replaceState: true });
          return;
        }
        goto(`/libraries/${detail.library_id}`, { replaceState: true });
        return;
      }
      show = detail;
      const list = await itemApi.children(id);
      // Newest episodes first. The scanner stores episode index from
      // file ordering; for date-stamped folders ("2024-04-01 - Title")
      // the natural order matches publication order so we reverse-sort.
      episodes = list.items.slice().sort((a, b) => {
        const ai = a.index ?? 0;
        const bi = b.index ?? 0;
        if (ai !== bi) return bi - ai;
        return b.title.localeCompare(a.title);
      });
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load podcast';
    } finally {
      loading = false;
    }
  }

  function dur(ms?: number): string {
    if (!ms) return '';
    const s = Math.floor(ms / 1000);
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    return h > 0 ? `${h}h ${m}m` : `${m}m`;
  }
</script>

<svelte:head><title>{show?.title ?? 'Podcast'} — OnScreen</title></svelte:head>

<div class="page">
  {#if loading}
    <p class="loading">Loading…</p>
  {:else if error}
    <p class="err">{error}</p>
  {:else if show}
    <nav class="crumb">
      <a href="/">Libraries</a>
      <span>/</span>
      <a href="/libraries/{show.library_id}">Podcasts</a>
      <span>/</span>
      <span>{show.title}</span>
    </nav>

    <header class="hero">
      {#if show.poster_path}
        <img class="hero-poster"
             src={assetUrl(`/artwork/${encodeURI(show.poster_path)}?v=${show.updated_at}&w=400`)}
             alt={show.title} />
      {:else}
        <div class="hero-poster placeholder">{show.title.charAt(0)}</div>
      {/if}
      <div class="hero-meta">
        <div class="kind">Podcast</div>
        <h1>{show.title}</h1>
        <div class="counts">
          {episodes.length} {episodes.length === 1 ? 'episode' : 'episodes'}
        </div>
        {#if show.summary}<p class="summary">{show.summary}</p>{/if}
      </div>
    </header>

    {#if episodes.length === 0}
      <div class="empty">
        <p>No episodes scanned yet. Drop episode files into this podcast's folder and rescan.</p>
      </div>
    {:else}
      <section class="episodes">
        <h2 class="section-title">Episodes</h2>
        <ul class="ep-list">
          {#each episodes as ep (ep.id)}
            <li>
              <a class="ep" href="/watch/{ep.id}">
                <div class="ep-thumb">
                  {#if ep.thumb_path}
                    <img src={assetUrl(`/artwork/${encodeURI(ep.thumb_path)}?v=${ep.updated_at}&w=200`)} alt={ep.title} loading="lazy" />
                  {:else if ep.poster_path}
                    <img src={assetUrl(`/artwork/${encodeURI(ep.poster_path)}?v=${ep.updated_at}&w=200`)} alt={ep.title} loading="lazy" />
                  {:else if show.poster_path}
                    <img src={assetUrl(`/artwork/${encodeURI(show.poster_path)}?v=${show.updated_at}&w=200`)} alt={ep.title} loading="lazy" />
                  {:else}
                    <div class="ep-thumb-blank">♪</div>
                  {/if}
                </div>
                <div class="ep-body">
                  <div class="ep-title">{ep.title}</div>
                  {#if ep.summary}<div class="ep-summary">{ep.summary}</div>{/if}
                  {#if ep.duration_ms}<div class="ep-meta">{dur(ep.duration_ms)}</div>{/if}
                </div>
              </a>
            </li>
          {/each}
        </ul>
      </section>
    {/if}
  {/if}
</div>

<style>
  .page { padding: 1.25rem 1.5rem 3rem; max-width: 1100px; margin: 0 auto; }
  .loading, .err { color: var(--text-muted); font-size: 0.9rem; }
  .err { color: var(--error); }

  .crumb { font-size: 0.78rem; color: var(--text-muted); margin-bottom: 1rem; }
  .crumb a { color: var(--text-muted); text-decoration: none; }
  .crumb a:hover { color: var(--text-secondary); }
  .crumb span { margin: 0 0.4rem; }

  .hero { display: flex; gap: 1.5rem; align-items: flex-start; margin-bottom: 2rem; }
  .hero-poster {
    width: 200px; height: 200px; object-fit: cover;
    border-radius: 8px; background: var(--bg-elevated); flex-shrink: 0;
  }
  .hero-poster.placeholder {
    display: flex; align-items: center; justify-content: center;
    color: var(--text-muted); font-size: 4rem;
  }
  .hero-meta { min-width: 0; flex: 1; }
  .kind {
    font-size: 0.7rem; color: var(--accent); text-transform: uppercase;
    letter-spacing: 0.08em; font-weight: 600; margin-bottom: 0.4rem;
  }
  .hero-meta h1 {
    font-size: 1.8rem; font-weight: 700; color: var(--text-primary);
    margin: 0 0 0.5rem; line-height: 1.1;
  }
  .counts { font-size: 0.85rem; color: var(--text-secondary); margin-bottom: 0.6rem; }
  .summary {
    font-size: 0.85rem; color: var(--text-secondary); line-height: 1.5;
    margin: 0; max-width: 70ch;
  }

  .empty {
    padding: 2rem 1rem; color: var(--text-muted); font-size: 0.85rem;
    text-align: center; background: var(--surface); border-radius: 8px;
  }

  .section-title {
    font-size: 0.95rem; font-weight: 600; color: var(--text-secondary);
    text-transform: uppercase; letter-spacing: 0.05em;
    margin: 0 0 0.75rem;
  }

  .ep-list { list-style: none; padding: 0; margin: 0; }
  .ep-list li { margin-bottom: 0.5rem; }

  .ep {
    display: flex; gap: 0.9rem; padding: 0.75rem;
    border-radius: 8px; text-decoration: none; color: inherit;
    background: var(--surface);
    transition: background 0.12s;
  }
  .ep:hover { background: var(--bg-elevated); }

  .ep-thumb {
    width: 96px; height: 96px; flex-shrink: 0; border-radius: 6px;
    overflow: hidden; background: var(--bg-elevated);
  }
  .ep-thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .ep-thumb-blank {
    width: 100%; height: 100%; display: flex; align-items: center;
    justify-content: center; color: var(--text-muted); font-size: 1.5rem;
  }

  .ep-body { min-width: 0; flex: 1; }
  .ep-title {
    font-size: 0.92rem; font-weight: 500; color: var(--text-primary);
    margin-bottom: 0.25rem;
  }
  .ep-summary {
    font-size: 0.78rem; color: var(--text-muted); line-height: 1.4;
    overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2;
    line-clamp: 2; -webkit-box-orient: vertical; margin-bottom: 0.4rem;
  }
  .ep-meta { font-size: 0.72rem; color: var(--text-muted); }

  @media (max-width: 600px) {
    .hero { flex-direction: column; align-items: center; text-align: center; }
    .hero-poster { width: 160px; height: 160px; }
    .ep-thumb { width: 64px; height: 64px; }
  }
</style>
