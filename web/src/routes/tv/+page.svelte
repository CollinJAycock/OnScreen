<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { liveTvApi, type LiveTVChannel, type LiveTVNowNext } from '$lib/api';

  let channels: LiveTVChannel[] = [];
  // Map channel_id -> at most two now/next entries (program order ASC).
  let nowNext: Record<string, LiveTVNowNext[]> = {};
  let loading = true;
  let error = '';
  let ready = false;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    ready = true;
    await load();
  });

  async function load() {
    loading = true; error = '';
    try {
      const [chRes, nnRes] = await Promise.all([
        liveTvApi.channels(),
        liveTvApi.nowNext(),
      ]);
      channels = chRes.items;
      // Server returns one row per (channel, program) pair, ordered by start.
      // Group on the client so each channel tile can render now + next.
      const grouped: Record<string, LiveTVNowNext[]> = {};
      for (const e of nnRes.items) {
        if (!grouped[e.channel_id]) grouped[e.channel_id] = [];
        grouped[e.channel_id].push(e);
      }
      nowNext = grouped;
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load TV';
    } finally { loading = false; }
  }

  function fmtTime(iso?: string): string {
    if (!iso) return '';
    const d = new Date(iso);
    return d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  }

  function progressPct(start?: string, end?: string): number {
    if (!start || !end) return 0;
    const s = new Date(start).getTime();
    const e = new Date(end).getTime();
    const now = Date.now();
    if (now <= s || e <= s) return 0;
    if (now >= e) return 100;
    return Math.round(((now - s) / (e - s)) * 100);
  }
</script>

<svelte:head><title>Live TV - OnScreen</title></svelte:head>

{#if ready}
<div class="page">
  <h1 class="page-title">Live TV</h1>

  {#if error}
    <div class="banner-error">{error}</div>
  {/if}

  {#if loading}
    <div class="grid">
      {#each [1,2,3,4,5,6,7,8,9] as _}
        <div class="skeleton-tile"></div>
      {/each}
    </div>
  {:else if channels.length === 0}
    <div class="empty">
      <p class="empty-title">No channels yet</p>
      <p class="empty-sub">Add a tuner in <a href="/settings/tv">Settings → Live TV</a> to scan channels.</p>
    </div>
  {:else}
    <div class="grid">
      {#each channels as ch (ch.id)}
        {@const programs = nowNext[ch.id] ?? []}
        {@const now = programs[0]}
        {@const next = programs[1]}
        <a class="tile" href="/tv/{ch.id}">
          <div class="tile-head">
            {#if ch.logo_url}
              <img class="logo" src={ch.logo_url} alt="" loading="lazy" />
            {:else}
              <div class="logo-blank">{ch.number}</div>
            {/if}
            <div class="ident">
              <div class="number">{ch.number}</div>
              <div class="name">{ch.name}</div>
            </div>
          </div>
          {#if now}
            <div class="program">
              <div class="program-title">{now.title ?? 'Now playing'}</div>
              <div class="program-time">
                {fmtTime(now.starts_at)} – {fmtTime(now.ends_at)}
              </div>
              <div class="progress"><div class="bar" style:width="{progressPct(now.starts_at, now.ends_at)}%"></div></div>
            </div>
            {#if next}
              <div class="next">
                <span class="next-label">Next:</span>
                {next.title ?? 'TBA'}
                <span class="next-time">{fmtTime(next.starts_at)}</span>
              </div>
            {/if}
          {:else}
            <div class="program program-empty">No guide data</div>
          {/if}
        </a>
      {/each}
    </div>
  {/if}
</div>
{/if}

<style>
  .page { padding: 2.5rem 2.5rem 4rem; max-width: 1400px; }
  .page-title { font-size: 1.1rem; font-weight: 700; color: var(--text-primary); letter-spacing: -0.02em; margin-bottom: 1.5rem; }

  .banner-error { background: var(--error-bg); border: 1px solid var(--error); color: var(--error); padding: 0.65rem 1rem; border-radius: 8px; font-size: 0.8rem; margin-bottom: 1.5rem; }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 1rem; }

  .tile {
    text-decoration: none; color: inherit;
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1rem;
    display: flex; flex-direction: column; gap: 0.6rem;
    transition: transform 0.15s, border-color 0.15s;
  }
  .tile:hover { transform: translateY(-2px); border-color: var(--accent); }

  .tile-head { display: flex; align-items: center; gap: 0.75rem; }
  .logo { width: 56px; height: 36px; object-fit: contain; background: #000; border-radius: 4px; }
  .logo-blank {
    width: 56px; height: 36px; display: flex; align-items: center; justify-content: center;
    font-weight: 700; color: var(--text-primary); background: var(--bg); border-radius: 4px;
    font-size: 0.85rem;
  }
  .ident { display: flex; flex-direction: column; }
  .number { font-size: 0.75rem; color: var(--text-muted); }
  .name { font-size: 0.95rem; font-weight: 600; color: var(--text-primary); }

  .program { display: flex; flex-direction: column; gap: 0.3rem; }
  .program-title { font-size: 0.85rem; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .program-time { font-size: 0.72rem; color: var(--text-muted); }
  .program-empty { font-size: 0.8rem; color: var(--text-muted); font-style: italic; }
  .progress { background: var(--bg); height: 3px; border-radius: 1.5px; overflow: hidden; }
  .bar { background: var(--accent); height: 100%; transition: width 0.3s; }

  .next { font-size: 0.72rem; color: var(--text-muted); display: flex; gap: 0.4rem; align-items: center; }
  .next-label { color: var(--text-muted); }
  .next-time { margin-left: auto; }

  .skeleton-tile { height: 130px; border-radius: 10px;
    background: linear-gradient(90deg, var(--bg-elevated) 25%, #16161f 50%, var(--bg-elevated) 75%);
    background-size: 200% 100%; animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  .empty { display: flex; flex-direction: column; align-items: center; text-align: center; padding: 6rem 2rem; gap: 0.5rem; }
  .empty-title { font-size: 1rem; font-weight: 600; color: var(--text-muted); }
  .empty-sub { font-size: 0.82rem; color: var(--text-muted); }
  .empty-sub a { color: var(--accent); }

  @media (max-width: 600px) { .page { padding: 1.25rem 1rem 3rem; } .grid { grid-template-columns: 1fr; } }
</style>
