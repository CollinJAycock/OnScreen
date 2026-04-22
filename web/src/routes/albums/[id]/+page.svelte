<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemApi, type ItemDetail, type ChildItem } from '$lib/api';
  import { audio, currentTrack, type AudioTrack } from '$lib/stores/audio';

  let album: ItemDetail | null = null;
  let tracks: ChildItem[] = [];
  let trackFiles: Map<string, string> = new Map(); // trackId -> fileId
  let artist: { id: string; title: string } | null = null;
  let loading = true;
  let error = '';

  $: id = $page.params.id!;
  $: nowPlayingId = $currentTrack?.id ?? null;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    await load();
  });

  $: if (id && album && id !== album.id) {
    load();
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const detail = await itemApi.get(id);
      if (detail.type !== 'album') {
        if (detail.type === 'artist') {
          goto(`/artists/${detail.id}`, { replaceState: true });
          return;
        }
        goto(`/libraries/${detail.library_id}`, { replaceState: true });
        return;
      }
      album = detail;

      // Parent (artist) — best-effort, used for the breadcrumb only.
      artist = null;
      if (detail.parent_id) {
        try {
          const a = await itemApi.get(detail.parent_id);
          artist = { id: a.id, title: a.title };
        } catch {
          // Non-fatal: orphan album just won't show artist breadcrumb.
        }
      }

      const list = await itemApi.children(id);
      tracks = list.items.sort((a, b) => (a.index ?? 9999) - (b.index ?? 9999));

      // Resolve file_id for every track in parallel. Tracks need a file_id to
      // stream; without it the play button is disabled.
      const map = new Map<string, string>();
      await Promise.all(tracks.map(async (t) => {
        try {
          const td = await itemApi.get(t.id);
          if (td.files.length > 0) map.set(t.id, td.files[0].id);
        } catch {
          // Track with no file — disabled in the UI.
        }
      }));
      trackFiles = map;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load album';
    } finally {
      loading = false;
    }
  }

  function buildQueue(startIdx: number): { queue: AudioTrack[]; index: number } {
    const queue: AudioTrack[] = [];
    let index = 0;
    for (let i = 0; i < tracks.length; i++) {
      const t = tracks[i];
      const fileId = trackFiles.get(t.id);
      if (!fileId) continue;
      if (i === startIdx) index = queue.length;
      queue.push({
        id: t.id,
        fileId,
        title: t.title,
        durationMS: t.duration_ms,
        index: t.index,
        album: album?.title,
        albumId: album?.id,
        artist: artist?.title,
        artistId: artist?.id,
        posterPath: album?.poster_path
      });
    }
    return { queue, index };
  }

  function playTrack(idx: number) {
    const t = tracks[idx];
    if (!trackFiles.has(t.id)) return;
    const { queue, index } = buildQueue(idx);
    audio.play(queue, index);
  }

  function playAlbum() {
    const firstPlayable = tracks.findIndex((t) => trackFiles.has(t.id));
    if (firstPlayable >= 0) playTrack(firstPlayable);
  }

  function formatDuration(ms?: number): string {
    if (!ms) return '';
    const s = Math.round(ms / 1000);
    const m = Math.floor(s / 60);
    return `${m}:${String(s % 60).padStart(2, '0')}`;
  }

  function totalDuration(): string {
    const total = tracks.reduce((sum, t) => sum + (t.duration_ms ?? 0), 0);
    if (!total) return '';
    const min = Math.floor(total / 60000);
    if (min < 60) return `${min} min`;
    return `${Math.floor(min / 60)}h ${min % 60}m`;
  }
</script>

<svelte:head><title>{album?.title ?? 'Album'} — OnScreen</title></svelte:head>

<div class="page">
  {#if loading}
    <p class="loading">Loading…</p>
  {:else if error}
    <p class="err">{error}</p>
  {:else if album}
    <nav class="crumb">
      <a href="/">Libraries</a>
      <span>/</span>
      <a href="/libraries/{album.library_id}">Music</a>
      {#if artist}
        <span>/</span>
        <a href="/artists/{artist.id}">{artist.title}</a>
      {/if}
      <span>/</span>
      <span>{album.title}</span>
    </nav>

    <header class="hero">
      {#if album.poster_path}
        <img class="hero-poster"
             src="/artwork/{encodeURI(album.poster_path)}?v={album.updated_at}&w=400"
             alt={album.title} />
      {:else}
        <div class="hero-poster placeholder">♪</div>
      {/if}
      <div class="hero-meta">
        <div class="kind">Album</div>
        <h1>{album.title}</h1>
        {#if artist}
          <div class="byline">by <a href="/artists/{artist.id}">{artist.title}</a></div>
        {/if}
        <div class="counts">
          {#if album.year}{album.year} · {/if}
          {tracks.length} {tracks.length === 1 ? 'track' : 'tracks'}
          {#if totalDuration()} · {totalDuration()}{/if}
        </div>
        <div class="actions">
          <button class="btn-play" on:click={playAlbum} disabled={trackFiles.size === 0}>
            <span class="ico">▶</span> Play
          </button>
        </div>
        {#if album.summary}<p class="bio">{album.summary}</p>{/if}
      </div>
    </header>

    {#if tracks.length === 0}
      <p class="empty">No tracks found.</p>
    {:else}
      <ol class="tracks">
        {#each tracks as t, i (t.id)}
          {@const playable = trackFiles.has(t.id)}
          {@const playing = nowPlayingId === t.id}
          <li class="row" class:playing class:disabled={!playable}>
            <button class="num" on:click={() => playTrack(i)} disabled={!playable}
                    title={playable ? `Play ${t.title}` : 'No file available'}>
              {#if playing}
                <span class="eq" aria-hidden="true">♫</span>
              {:else}
                <span class="num-text">{t.index ?? i + 1}</span>
                <span class="num-play" aria-hidden="true">▶</span>
              {/if}
            </button>
            <div class="title">{t.title}</div>
            <div class="dur">{formatDuration(t.duration_ms)}</div>
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

  .actions { margin-bottom: 1rem; }
  .btn-play {
    display: inline-flex; align-items: center; gap: 0.5rem;
    background: var(--accent); color: white; border: 0; padding: 0.6rem 1.4rem;
    border-radius: 999px; font-size: 0.9rem; font-weight: 600; cursor: pointer;
  }
  .btn-play:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-play:hover:not(:disabled) { filter: brightness(1.1); }
  .btn-play .ico { font-size: 0.7rem; }

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
