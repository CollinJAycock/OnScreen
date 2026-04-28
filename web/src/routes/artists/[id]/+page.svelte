<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemApi, assetUrl, type ItemDetail, type ChildItem } from '$lib/api';

  let artist: ItemDetail | null = null;
  let albums: ChildItem[] = [];
  let musicVideos: ChildItem[] = [];
  let loading = true;
  let error = '';

  $: id = $page.params.id!;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    await load();
  });

  $: if (id && artist && id !== artist.id) {
    load();
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const detail = await itemApi.get(id);
      if (detail.type !== 'artist') {
        // Wrong type for this route — redirect to a more sensible place.
        if (detail.type === 'album') {
          goto(`/albums/${detail.id}`, { replaceState: true });
          return;
        }
        goto(`/libraries/${detail.library_id}`, { replaceState: true });
        return;
      }
      artist = detail;
      const list = await itemApi.children(id);
      // Split the children into albums and music videos — both hang
      // off the artist but render as separate rows on the page.
      // Music videos have no discography hierarchy (no album node),
      // just a flat list under the artist.
      const all = list.items;
      albums = all.filter(c => c.type === 'album').sort((a, b) => {
        const ya = a.year ?? 0;
        const yb = b.year ?? 0;
        if (ya !== yb) return yb - ya; // newest first
        return a.title.localeCompare(b.title);
      });
      musicVideos = all.filter(c => c.type === 'music_video').sort((a, b) =>
        a.title.localeCompare(b.title),
      );
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load artist';
    } finally {
      loading = false;
    }
  }
</script>

<svelte:head><title>{artist?.title ?? 'Artist'} — OnScreen</title></svelte:head>

<div class="page">
  {#if loading}
    <p class="loading">Loading…</p>
  {:else if error}
    <p class="err">{error}</p>
  {:else if artist}
    <nav class="crumb">
      <a href="/">Libraries</a>
      <span>/</span>
      <a href="/libraries/{artist.library_id}">Music</a>
      <span>/</span>
      <span>{artist.title}</span>
    </nav>

    <header class="hero">
      {#if artist.poster_path}
        <img class="hero-poster"
             src={assetUrl(`/artwork/${encodeURI(artist.poster_path)}?v=${artist.updated_at}&w=400`)}
             alt={artist.title} />
      {:else}
        <div class="hero-poster placeholder">{artist.title.charAt(0)}</div>
      {/if}
      <div class="hero-meta">
        <div class="kind">Artist</div>
        <h1>{artist.title}</h1>
        <div class="counts">
          {albums.length} {albums.length === 1 ? 'album' : 'albums'}
          {#if musicVideos.length > 0}
            · {musicVideos.length} music {musicVideos.length === 1 ? 'video' : 'videos'}
          {/if}
        </div>
        {#if artist.summary}
          <p class="bio">{artist.summary}</p>
        {/if}
      </div>
    </header>

    {#if albums.length === 0 && musicVideos.length === 0}
      <p class="empty">No albums or music videos in your library yet.</p>
    {/if}

    {#if albums.length > 0}
      <h2 class="section-h">Albums</h2>
      <div class="grid">
        {#each albums as a (a.id)}
          <a class="card" href="/albums/{a.id}">
            <div class="poster">
              {#if a.poster_path}
                <img src={assetUrl(`/artwork/${encodeURI(a.poster_path)}?v=${a.updated_at}&w=300`)}
                     srcset="{assetUrl(`/artwork/${encodeURI(a.poster_path)}?v=${a.updated_at}&w=150`)} 150w, {assetUrl(`/artwork/${encodeURI(a.poster_path)}?v=${a.updated_at}&w=300`)} 300w, {assetUrl(`/artwork/${encodeURI(a.poster_path)}?v=${a.updated_at}&w=450`)} 450w"
                     sizes="(max-width: 768px) 100px, 180px"
                     alt={a.title} loading="lazy" />
              {:else}
                <div class="poster-blank">♪</div>
              {/if}
            </div>
            <div class="title">{a.title}</div>
            {#if a.year}<div class="year">{a.year}</div>{/if}
          </a>
        {/each}
      </div>
    {/if}

    {#if musicVideos.length > 0}
      <h2 class="section-h mv-h">Music Videos</h2>
      <div class="grid mv-grid">
        {#each musicVideos as v (v.id)}
          <a class="card" href="/watch/{v.id}">
            <div class="poster mv-poster">
              {#if v.thumb_path}
                <img src={assetUrl(`/artwork/${encodeURI(v.thumb_path)}?v=${v.updated_at}&w=400`)}
                     alt={v.title} loading="lazy" />
              {:else if v.poster_path}
                <img src={assetUrl(`/artwork/${encodeURI(v.poster_path)}?v=${v.updated_at}&w=400`)}
                     alt={v.title} loading="lazy" />
              {:else}
                <div class="poster-blank">▶</div>
              {/if}
            </div>
            <div class="title">{v.title}</div>
            {#if v.year}<div class="year">{v.year}</div>{/if}
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

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 1.5rem 1rem;
  }

  .card { text-decoration: none; color: inherit; display: block; }
  .poster {
    aspect-ratio: 1 / 1; border-radius: 6px; overflow: hidden;
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

  /* Music-video row sits below albums with a 16:9 poster so it reads
     visually as video (albums are square). Slightly wider minmax so
     the same breakpoint renders fewer but larger thumbnails. */
  .mv-h { margin-top: 2.5rem; }
  .mv-grid { grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); }
  .mv-poster { aspect-ratio: 16 / 9; }

  @media (max-width: 600px) {
    .page { padding: 1.5rem 1rem 4rem; }
    .hero { flex-direction: column; align-items: flex-start; gap: 1rem; }
    .hero-poster { width: 140px; height: 140px; }
    .hero-meta h1 { font-size: 1.6rem; }
  }
</style>
