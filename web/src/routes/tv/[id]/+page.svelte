<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import Hls from 'hls.js';
  import { liveTvApi, type LiveTVChannel, type LiveTVNowNext } from '$lib/api';

  $: id = $page.params.id!;

  let channels: LiveTVChannel[] = [];
  let channel: LiveTVChannel | null = null;
  let nowNext: LiveTVNowNext[] = [];
  let videoEl: HTMLVideoElement;
  let hls: Hls | null = null;
  let error = '';
  let loading = true;
  let ready = false;

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    ready = true;
    await load();
  });

  onDestroy(() => {
    if (hls) { hls.destroy(); hls = null; }
  });

  $: if (id && ready && channels.length > 0) {
    // Re-attach when navigating between channels (e.g. P key).
    void switchChannel(id);
  }

  async function load() {
    loading = true; error = '';
    try {
      const [chRes, nnRes] = await Promise.all([
        liveTvApi.channels(),
        liveTvApi.nowNext(),
      ]);
      channels = chRes.items;
      const found = channels.find(c => c.id === id);
      if (!found) { error = 'Channel not found'; return; }
      channel = found;
      nowNext = nnRes.items.filter(e => e.channel_id === id);
      await attachStream(id);
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load channel';
    } finally { loading = false; }
  }

  async function switchChannel(channelId: string) {
    const found = channels.find(c => c.id === channelId);
    if (!found) return;
    channel = found;
    nowNext = nowNext.filter(e => e.channel_id === channelId); // best-effort; reload below refreshes
    await attachStream(channelId);
    try {
      const nnRes = await liveTvApi.nowNext();
      nowNext = nnRes.items.filter(e => e.channel_id === channelId);
    } catch {}
  }

  async function attachStream(channelId: string) {
    if (!videoEl) return;
    if (hls) { hls.destroy(); hls = null; }
    error = '';

    const url = liveTvApi.streamUrl(channelId);
    if (Hls.isSupported()) {
      hls = new Hls({ liveDurationInfinity: true, lowLatencyMode: false });
      hls.loadSource(url);
      hls.attachMedia(videoEl);
      hls.on(Hls.Events.ERROR, (_event, data) => {
        if (data.fatal) {
          error = data.details || 'Stream error';
        }
      });
    } else if (videoEl.canPlayType('application/vnd.apple.mpegurl')) {
      // Safari: native HLS.
      videoEl.src = url;
    } else {
      error = 'Browser does not support HLS playback';
    }
  }

  function prevChannel() {
    if (!channel) return;
    const i = channels.findIndex(c => c.id === channel!.id);
    if (i > 0) goto(`/tv/${channels[i - 1].id}`);
  }
  function nextChannel() {
    if (!channel) return;
    const i = channels.findIndex(c => c.id === channel!.id);
    if (i >= 0 && i < channels.length - 1) goto(`/tv/${channels[i + 1].id}`);
  }

  function fmtTime(iso?: string): string {
    if (!iso) return '';
    return new Date(iso).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  }
  function progressPct(start?: string, end?: string): number {
    if (!start || !end) return 0;
    const s = new Date(start).getTime(), e = new Date(end).getTime(), now = Date.now();
    if (now <= s || e <= s) return 0;
    if (now >= e) return 100;
    return Math.round(((now - s) / (e - s)) * 100);
  }

  function onKey(e: KeyboardEvent) {
    if (e.target instanceof HTMLInputElement) return;
    if (e.key === 'p' || e.key === 'P') { prevChannel(); }
    if (e.key === 'n' || e.key === 'N') { nextChannel(); }
  }
</script>

<svelte:head><title>{channel?.name ?? 'Live TV'} - OnScreen</title></svelte:head>
<svelte:window on:keydown={onKey} />

{#if ready}
<div class="page">
  {#if error}
    <div class="banner-error">{error}</div>
  {/if}

  {#if loading}
    <div class="loading">Loading channel…</div>
  {:else if channel}
    <div class="player-wrap">
      <video bind:this={videoEl} controls autoplay muted playsinline></video>
    </div>

    <div class="info-bar">
      <div class="ident">
        {#if channel.logo_url}<img class="logo" src={channel.logo_url} alt="" />{/if}
        <div class="ident-text">
          <div class="number">{channel.number}</div>
          <div class="name">{channel.name}</div>
        </div>
      </div>
      <div class="program-block">
        {#if nowNext[0]}
          <div class="program-title">{nowNext[0].title ?? 'Now playing'}</div>
          <div class="program-meta">{fmtTime(nowNext[0].starts_at)} – {fmtTime(nowNext[0].ends_at)}</div>
          <div class="progress"><div class="bar" style:width="{progressPct(nowNext[0].starts_at, nowNext[0].ends_at)}%"></div></div>
          {#if nowNext[1]}
            <div class="next">Next: <strong>{nowNext[1].title}</strong> at {fmtTime(nowNext[1].starts_at)}</div>
          {/if}
        {:else}
          <div class="program-meta">No guide data for this channel</div>
        {/if}
      </div>
      <div class="controls">
        <button class="btn" on:click={prevChannel}>↑ Prev</button>
        <button class="btn" on:click={nextChannel}>↓ Next</button>
        <a class="btn" href="/tv">All channels</a>
      </div>
    </div>

    <p class="hint">Tip: press <kbd>P</kbd> for previous channel, <kbd>N</kbd> for next.</p>
  {/if}
</div>
{/if}

<style>
  .page { padding: 1.5rem; max-width: 1400px; margin: 0 auto; }
  .banner-error { background: var(--error-bg); border: 1px solid var(--error); color: var(--error); padding: 0.65rem 1rem; border-radius: 8px; margin-bottom: 1rem; }
  .loading { padding: 4rem; text-align: center; color: var(--text-muted); }

  .player-wrap { background: #000; border-radius: 10px; overflow: hidden; aspect-ratio: 16 / 9; }
  .player-wrap video { width: 100%; height: 100%; display: block; }

  .info-bar {
    display: grid;
    grid-template-columns: auto 1fr auto;
    gap: 1.5rem;
    align-items: center;
    background: var(--bg-elevated);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1rem 1.25rem;
    margin-top: 1rem;
  }
  .ident { display: flex; align-items: center; gap: 0.75rem; }
  .logo { width: 64px; height: 40px; object-fit: contain; background: #000; border-radius: 4px; }
  .ident-text { display: flex; flex-direction: column; }
  .number { color: var(--text-muted); font-size: 0.8rem; }
  .name { color: var(--text-primary); font-weight: 600; }

  .program-block { display: flex; flex-direction: column; gap: 0.3rem; }
  .program-title { font-size: 1rem; color: var(--text-primary); }
  .program-meta { font-size: 0.78rem; color: var(--text-muted); }
  .progress { background: var(--bg); height: 4px; border-radius: 2px; overflow: hidden; }
  .bar { background: var(--accent); height: 100%; transition: width 0.5s; }
  .next { font-size: 0.78rem; color: var(--text-muted); }

  .controls { display: flex; gap: 0.5rem; }
  .btn {
    padding: 0.5rem 0.85rem; background: var(--bg);
    color: var(--text-primary); border: 1px solid var(--border);
    border-radius: 6px; font-size: 0.85rem; cursor: pointer; text-decoration: none;
  }
  .btn:hover { background: var(--bg-hover); }

  .hint { color: var(--text-muted); font-size: 0.78rem; margin-top: 0.75rem; text-align: center; }
  kbd { background: var(--bg-elevated); border: 1px solid var(--border); padding: 0.05rem 0.35rem; border-radius: 3px; font-family: monospace; font-size: 0.78rem; }

  @media (max-width: 800px) {
    .info-bar { grid-template-columns: 1fr; gap: 1rem; }
    .controls { justify-content: stretch; }
    .btn { flex: 1; text-align: center; }
  }
</style>
