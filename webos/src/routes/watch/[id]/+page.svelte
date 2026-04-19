<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import {
    api,
    endpoints,
    Unauthorized,
    type ItemDetail,
    type Chapter
  } from '$lib/api';
  import { focusManager } from '$lib/focus/manager';
  import type { RemoteKey } from '$lib/focus/keys';
  import { loadHls } from '$lib/player/hls-loader';
  import { ProgressReporter } from '$lib/player/progress-reporter';

  const itemID = page.params.id!;
  let video: HTMLVideoElement | undefined = $state();

  let item = $state<ItemDetail | null>(null);
  let error = $state('');
  let loading = $state(true);
  let paused = $state(true);
  let position = $state(0);
  let duration = $state(0);
  let controlsVisible = $state(true);
  let controlsTimer: ReturnType<typeof setTimeout> | null = null;

  let hls: { destroy: () => void } | null = null;
  let session: { session_id: string; token: string; playlist_url: string } | null = null;
  let reporter: ProgressReporter | null = null;

  // Chapters: surface as jump targets. Start offsets used for green-button cycling.
  const chapters = $derived<Chapter[]>(item?.files[0]?.chapters ?? []);

  function showControls() {
    controlsVisible = true;
    if (controlsTimer) clearTimeout(controlsTimer);
    controlsTimer = setTimeout(() => (controlsVisible = false), 3000);
  }

  function fmt(ms: number): string {
    const s = Math.max(0, Math.floor(ms / 1000));
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const sec = s % 60;
    return h > 0
      ? `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}`
      : `${m}:${String(sec).padStart(2, '0')}`;
  }

  function seek(deltaMs: number) {
    if (!video) return;
    const pos = Math.max(0, Math.min(duration, position + deltaMs));
    video.currentTime = pos / 1000;
    showControls();
  }

  function togglePlay() {
    if (!video) return;
    if (video.paused) void video.play();
    else video.pause();
    showControls();
  }

  function jumpToChapter(dir: 1 | -1) {
    if (chapters.length === 0 || !video) return;
    const idx = chapters.findIndex((c) => c.start_ms > position + 2000 * dir);
    let target = dir === 1 ? idx : idx === -1 ? chapters.length - 1 : Math.max(0, idx - 1);
    if (target < 0) target = 0;
    const ch = chapters[target];
    if (ch) video.currentTime = ch.start_ms / 1000;
    showControls();
  }

  async function stopAndLeave() {
    if (reporter) reporter.stopped(position, duration);
    if (session && api.getToken()) {
      void endpoints.transcode.stop(session.session_id, session.token).catch(() => {});
    }
    if (hls) hls.destroy();
    goto(`/item/${itemID}`);
  }

  function onKey(k: RemoteKey): boolean {
    switch (k) {
      case 'back':
        void stopAndLeave();
        return true;
      case 'enter':
      case 'playpause':
        togglePlay();
        return true;
      case 'play':
        if (video?.paused) void video.play();
        return true;
      case 'pause':
        if (!video?.paused) video?.pause();
        return true;
      case 'left':
        seek(-10_000);
        return true;
      case 'right':
        seek(10_000);
        return true;
      case 'rewind':
        seek(-30_000);
        return true;
      case 'forward':
        seek(30_000);
        return true;
      case 'green':
        jumpToChapter(1);
        return true;
      case 'red':
        jumpToChapter(-1);
        return true;
    }
    return false;
  }

  onMount(() => {
    const offKey = focusManager.pushKeyHandler(onKey);

    (async () => {
      try {
        item = await endpoints.items.get(itemID);
        if (item.files.length === 0) {
          error = 'No playable file for this item.';
          loading = false;
          return;
        }

        const file = item.files[0];
        const startMs = item.view_offset_ms ?? 0;

        session = await endpoints.transcode.start({
          itemId: itemID,
          height: 1080,
          positionMs: startMs,
          fileId: file.id,
          supportsHEVC: true
        });

        const Hls = await loadHls();
        const fullURL = session.playlist_url.startsWith('http')
          ? session.playlist_url
          : api.mediaUrl(session.playlist_url);

        if (Hls.isSupported()) {
          const hlsInst = new Hls({ lowLatencyMode: false });
          hlsInst.loadSource(fullURL);
          hlsInst.attachMedia(video!);
          hls = hlsInst;
        } else if (video!.canPlayType('application/vnd.apple.mpegurl')) {
          video!.src = fullURL;
        } else {
          error = 'HLS is not supported on this device.';
          loading = false;
          return;
        }

        reporter = new ProgressReporter(itemID);
        reporter.start(() => ({ positionMs: position, durationMs: duration }));

        video!.addEventListener('loadedmetadata', () => {
          if (startMs > 0 && video) video.currentTime = startMs / 1000;
          loading = false;
          void video?.play();
          showControls();
        });
        video!.addEventListener('timeupdate', () => {
          position = Math.round((video?.currentTime ?? 0) * 1000);
          duration = Math.round((video?.duration ?? 0) * 1000);
        });
        video!.addEventListener('pause', () => {
          paused = true;
          reporter?.paused(position, duration);
          showControls();
        });
        video!.addEventListener('play', () => {
          paused = false;
        });
        video!.addEventListener('ended', () => {
          reporter?.stopped(duration, duration);
          goto(`/item/${itemID}`);
        });
      } catch (e) {
        if (e instanceof Unauthorized) goto('/login');
        else {
          error = (e as Error).message;
          loading = false;
        }
      }
    })();

    return () => {
      offKey();
      reporter?.stopped(position, duration);
      if (session && api.getToken()) {
        void endpoints.transcode.stop(session.session_id, session.token).catch(() => {});
      }
      hls?.destroy();
      if (controlsTimer) clearTimeout(controlsTimer);
    };
  });

  onDestroy(() => {
    hls?.destroy();
  });

  const progressPct = $derived(duration > 0 ? (position / duration) * 100 : 0);
</script>

<div class="player" onmousemove={showControls}>
  <!-- svelte-ignore a11y_media_has_caption -->
  <video bind:this={video} class="video" playsinline></video>

  {#if loading}
    <div class="overlay center">
      <div class="title">Starting playback…</div>
      {#if item}<div class="sub">{item.title}</div>{/if}
    </div>
  {:else if error}
    <div class="overlay center">
      <div class="title error">{error}</div>
    </div>
  {/if}

  {#if controlsVisible && !loading && !error}
    <div class="controls">
      <div class="top">
        {#if item}<div class="now-playing">{item.title}</div>{/if}
      </div>

      <div class="bottom">
        <div class="state">{paused ? '❚❚ paused' : '▶ playing'}</div>
        <div class="bar">
          <div class="elapsed">{fmt(position)}</div>
          <div class="track">
            <div class="fill" style="width: {progressPct}%"></div>
            {#each chapters as ch (ch.start_ms)}
              {#if duration > 0}
                <div class="chapter-marker" style="left: {(ch.start_ms / duration) * 100}%"></div>
              {/if}
            {/each}
          </div>
          <div class="remaining">{fmt(duration - position)}</div>
        </div>

        <div class="hints">
          <span>← → seek 10s</span>
          <span>◀◀ ▶▶ seek 30s</span>
          <span>OK play/pause</span>
          {#if chapters.length > 0}<span>red/green chapters</span>{/if}
          <span>back exit</span>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .player {
    position: fixed;
    inset: 0;
    background: #000;
    overflow: hidden;
  }

  .video {
    width: 100%;
    height: 100%;
    object-fit: contain;
  }

  .overlay {
    position: absolute;
    inset: 0;
  }

  .overlay.center {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    background: rgba(0, 0, 0, 0.7);
    gap: 20px;
  }

  .overlay .title {
    font-size: var(--font-xl);
    color: white;
  }

  .overlay .title.error {
    color: #fca5a5;
  }

  .overlay .sub {
    font-size: var(--font-md);
    color: var(--text-secondary);
  }

  .controls {
    position: absolute;
    inset: 0;
    pointer-events: none;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
  }

  .top {
    background: linear-gradient(180deg, rgba(0,0,0,0.7), transparent);
    padding: 48px 80px 80px;
  }

  .now-playing {
    font-size: var(--font-xl);
    color: white;
  }

  .bottom {
    background: linear-gradient(0deg, rgba(0,0,0,0.85), transparent);
    padding: 80px 80px 48px;
  }

  .state {
    font-size: var(--font-md);
    color: white;
    margin-bottom: 24px;
  }

  .bar {
    display: grid;
    grid-template-columns: auto 1fr auto;
    align-items: center;
    gap: 24px;
    color: white;
    font-size: var(--font-md);
  }

  .track {
    position: relative;
    height: 8px;
    background: rgba(255, 255, 255, 0.25);
    border-radius: 4px;
    overflow: visible;
  }

  .fill {
    height: 100%;
    background: var(--accent);
    border-radius: 4px;
  }

  .chapter-marker {
    position: absolute;
    top: -4px;
    width: 2px;
    height: 16px;
    background: white;
    transform: translateX(-1px);
  }

  .hints {
    margin-top: 24px;
    display: flex;
    gap: 32px;
    font-size: var(--font-sm);
    color: rgba(255, 255, 255, 0.6);
  }
</style>
