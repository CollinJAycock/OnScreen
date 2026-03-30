<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { itemApi, transcodeApi, type ItemDetail, type ChildItem, type ItemFile, type MatchCandidate } from '$lib/api';
  import Hls from 'hls.js';

  $: id = $page.params.id;

  let mounted = false;
  let prevId = '';

  let item: ItemDetail | null = null;
  let siblings: ChildItem[] = []; // other episodes in the same season
  let loading = true;
  let error = '';

  // Video element reference
  let videoEl: HTMLVideoElement;
  let containerEl: HTMLDivElement;

  // Playback state
  let paused = true;
  let currentTime = 0;
  let duration = 0;
  let volume = 1;
  let muted = false;
  let buffered = 0; // 0-1
  let fullscreen = false;
  let ended = false;

  // Controls visibility
  let showControls = true;
  let hideTimer: ReturnType<typeof setTimeout> | null = null;

  // Seeking via drag
  let seeking = false;
  let seekBarEl: HTMLDivElement;

  // Progress save timer
  let progressTimer: ReturnType<typeof setInterval> | null = null;

  // Quality picker
  type QualityOption = { label: string; height: number };
  const qualityOptions: QualityOption[] = [
    { label: 'Auto (Direct Play)', height: 0 },
    { label: '4K (2160p)',         height: 2160 },
    { label: '1080p',              height: 1080 },
    { label: '720p',               height: 720 },
    { label: '480p',               height: 480 },
    { label: '360p',               height: 360 },
  ];
  let selectedQuality: QualityOption = qualityOptions[0];
  let showQualityMenu = false;

  // Skip the auto-seek in onVideoLoaded during quality switches
  let skipAutoSeek = false;

  // Audio codecs that browsers can decode natively.
  // Everything else (DTS, TrueHD, etc.) needs a transcode pass.
  const browserAudioCodecs = new Set([
    'aac', 'mp3', 'opus', 'flac', 'vorbis', 'alac',
    'mp2', 'pcm_s16le', 'pcm_f32le',
  ]);
  // Containers browsers handle reliably for direct play.
  const browserContainers = new Set(['mp4', 'webm', 'mov']);
  // Video codecs browsers can decode natively (H.264 universally, VP8/VP9 most browsers).
  const browserVideoCodecs = new Set(['h264', 'vp8', 'vp9', 'av1']);

  /** True when the browser can play this file directly — compatible container + codecs. */
  function canDirectPlay(file: ItemFile | undefined): boolean {
    if (!file) return false;
    const container = (file.container ?? '').toLowerCase();
    const videoCodec = (file.video_codec ?? '').toLowerCase();
    const audioCodec = (file.audio_codec ?? '').toLowerCase();
    if (!browserContainers.has(container)) return false;
    if (videoCodec && !browserVideoCodecs.has(videoCodec)) return false;
    if (audioCodec && !browserAudioCodecs.has(audioCodec)) return false;
    return true;
  }

  /** True when the video can be stream-copied (remuxed) instead of re-encoded. */
  function canRemuxVideo(file: ItemFile | undefined): boolean {
    if (!file) return false;
    const videoCodec = (file.video_codec ?? '').toLowerCase();
    return browserVideoCodecs.has(videoCodec);
  }

  // HLS transcode state
  let hlsInstance: Hls | null = null;
  let transcodeSessionId: string | null = null;
  let transcodeToken: string | null = null;
  // Offset (seconds) of where the current HLS stream starts within the content.
  // videoEl.currentTime + hlsOffsetSec = real content position.
  let hlsOffsetSec = 0;
  // True while an HLS session is active. Needed to distinguish HLS-at-offset-0
  // from direct play (both have hlsOffsetSec === 0).
  let hlsActive = false;

  // Reactive: available quality options filtered to source resolution
  $: sourceHeight = item?.files?.[0]?.resolution_h ?? 0;
  $: availableQualities = qualityOptions.filter(
    q => q.height === 0 || sourceHeight === 0 || q.height <= sourceHeight
  );

  $: nextEpisode = (() => {
    if (!item || item.type !== 'episode' || item.index == null) return null;
    return siblings.find(s => s.index != null && s.index === (item!.index! + 1)) ?? null;
  })();

  onMount(async () => {
    if (!localStorage.getItem('onscreen_user')) { goto('/login'); return; }
    prevId = id;
    await load();
    mounted = true;
  });

  $: if (mounted && id && id !== prevId) {
    prevId = id;
    clearTimers();
    if (videoEl && !videoEl.paused) saveProgress('stopped');
    stopTranscodeSession();
    destroyHls();
    item = null;
    siblings = [];
    hlsOffsetSec = 0;
    hlsActive = false;
    selectedQuality = qualityOptions[0];
    skipAutoSeek = false;
    ended = false;
    error = '';
    loading = true;
    transcodeSessionId = null;
    transcodeToken = null;
    load();
  }

  onDestroy(() => {
    clearTimers();
    window.removeEventListener('mousemove', onSeekMouseMove);
    window.removeEventListener('mouseup', onSeekMouseUp);
    if (videoEl && !videoEl.paused && item) {
      const body = JSON.stringify({
        view_offset_ms: Math.round((videoEl.currentTime + hlsOffsetSec) * 1000),
        duration_ms: item.duration_ms ?? 0,
        state: 'stopped'
      });
      fetch(`/api/v1/items/${item.id}/progress`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body,
        keepalive: true,
        credentials: 'same-origin'
      }).catch(() => {});
    }
    stopTranscodeSession();
    destroyHls();
  });

  // ── Detail view state (shows / seasons) ──────────────────────────────────
  let seasons: ChildItem[] = [];
  let seasonEpisodes: Map<string, ChildItem[]> = new Map();
  let selectedSeasonId: string | null = null;
  $: isDetailView = item != null && (item.type === 'show' || item.type === 'season');
  $: selectedEpisodes = selectedSeasonId ? (seasonEpisodes.get(selectedSeasonId) ?? []) : [];

  // ── Fix Match modal state ──────────────────────────────────────────────────
  let showMatchModal = false;
  let matchQuery = '';
  let matchCandidates: MatchCandidate[] = [];
  let matchSearching = false;
  let matchApplying = false;
  let matchError = '';

  async function openMatchModal() {
    showMatchModal = true;
    matchQuery = item?.title ?? '';
    matchCandidates = [];
    matchError = '';
    matchSearching = false;
    matchApplying = false;
    // Auto-search with current title.
    if (matchQuery && item) {
      await searchMatch();
    }
  }

  async function searchMatch() {
    if (!item || !matchQuery.trim()) return;
    matchSearching = true;
    matchError = '';
    try {
      matchCandidates = await itemApi.searchMatch(item.id, matchQuery.trim());
    } catch (e: unknown) {
      matchError = e instanceof Error ? e.message : 'Search failed';
    } finally {
      matchSearching = false;
    }
  }

  async function applyMatch(tmdbId: number) {
    if (!item) return;
    matchApplying = true;
    matchError = '';
    try {
      await itemApi.applyMatch(item.id, tmdbId);
      showMatchModal = false;
      // Reload the page after a short delay to let enrichment finish.
      setTimeout(() => { load(); }, 2000);
    } catch (e: unknown) {
      matchError = e instanceof Error ? e.message : 'Failed to apply match';
    } finally {
      matchApplying = false;
    }
  }

  async function loadShowDetail() {
    if (!item) return;

    if (item.type === 'show') {
      const r = await itemApi.children(item.id);
      seasons = r.items
        .filter(c => c.type === 'season')
        .sort((a, b) => (a.index ?? 0) - (b.index ?? 0));

      // Auto-select the first season and load its episodes.
      if (seasons.length > 0) {
        await selectSeason(seasons[0].id);
      }
    } else if (item.type === 'season') {
      // Landed directly on a season — load its episodes.
      const r = await itemApi.children(item.id);
      const eps = r.items
        .filter(c => c.type === 'episode')
        .sort((a, b) => (a.index ?? 0) - (b.index ?? 0));
      seasons = [{ ...item as unknown as ChildItem }]; // single-season view
      seasonEpisodes.set(item.id, eps);
      selectedSeasonId = item.id;
    }
  }

  async function selectSeason(seasonId: string) {
    selectedSeasonId = seasonId;
    if (!seasonEpisodes.has(seasonId)) {
      const r = await itemApi.children(seasonId);
      const eps = r.items
        .filter(c => c.type === 'episode')
        .sort((a, b) => (a.index ?? 0) - (b.index ?? 0));
      seasonEpisodes.set(seasonId, eps);
      seasonEpisodes = seasonEpisodes; // trigger reactivity
    }
  }

  async function load() {
    loading = true;
    error = '';
    try {
      item = await itemApi.get(id);

      // Shows and seasons: load detail view instead of trying to play.
      if (item.type === 'show' || item.type === 'season') {
        await loadShowDetail();
        return;
      }

      if (item.type === 'episode' && item.parent_id) {
        const r = await itemApi.children(item.parent_id);
        siblings = r.items.sort((a, b) => (a.index ?? 0) - (b.index ?? 0));
      }
    } catch (e: unknown) {
      error = e instanceof Error ? e.message : 'Failed to load';
    } finally {
      loading = false;
    }
    // Wait for Svelte to render the <video> element (gated on item && streamURL).
    await tick();
    if (!item?.files?.[0]?.stream_url || !videoEl) return;

    const file = item.files[0];
    // Signal intent to auto-play so controls don't flash a paused state.
    paused = false;
    if (canDirectPlay(file)) {
      // Direct play — browser handles container + codecs natively.
      videoEl.src = file.stream_url;
      videoEl.load();
    } else if (canRemuxVideo(file)) {
      // Video is browser-compatible (H.264) but audio or container is not.
      // Stream-copy the video and only transcode audio → fast, lossless video.
      const posMs = item.view_offset_ms > 0 ? item.view_offset_ms : 0;
      await switchToTranscode(0, posMs, true);
    } else {
      // Full transcode needed (non-browser video codec like HEVC/VC-1/MPEG-2).
      const height = file.resolution_h ?? 1080;
      const match = availableQualities.find(q => q.height === height)
                 ?? availableQualities.find(q => q.height > 0)
                 ?? qualityOptions[2]; // 1080p fallback
      selectedQuality = match;
      const posMs = item.view_offset_ms > 0 ? item.view_offset_ms : 0;
      await switchToTranscode(match.height, posMs);
    }
  }

  // ── Quality switching ────────────────────────────────────────────────────────

  async function selectQuality(q: QualityOption) {
    if (q === selectedQuality) { showQualityMenu = false; return; }
    selectedQuality = q;
    showQualityMenu = false;

    if (!item || !item.files?.length) return;

    const posMs = Math.floor(currentTime * 1000); // currentTime is content-relative

    skipAutoSeek = true;
    if (q.height === 0) {
      const file = item.files[0];
      if (canDirectPlay(file)) {
        await switchToDirectPlay(posMs);
      } else if (canRemuxVideo(file)) {
        // "Auto" but can't direct-play → remux (video copy + audio transcode).
        await switchToTranscode(0, posMs, true);
      } else {
        // "Auto" but nothing is browser-compatible → full transcode at source res.
        const h = file.resolution_h ?? 1080;
        await switchToTranscode(h, posMs);
      }
    } else {
      // Explicit resolution selected — full transcode.
      await switchToTranscode(q.height, posMs);
    }
  }

  async function switchToDirectPlay(posMs: number) {
    const wasPlaying = videoEl && !videoEl.paused;
    destroyHls();
    await stopTranscodeSession();
    hlsOffsetSec = 0;
    hlsActive = false;

    const file = item!.files[0];
    videoEl.src = file.stream_url;
    videoEl.load();

    // Restore position once metadata loads.
    const restorePos = () => {
      videoEl.currentTime = posMs / 1000;
      if (wasPlaying) videoEl.play().catch(() => {});
      videoEl.removeEventListener('loadedmetadata', restorePos);
    };
    videoEl.addEventListener('loadedmetadata', restorePos);
  }

  async function switchToTranscode(height: number, posMs: number, videoCopy: boolean = false) {
    if (!item) return;
    const wasPlaying = videoEl && !videoEl.paused;
    await stopTranscodeSession();
    destroyHls();

    try {
      const sess = await transcodeApi.start(item.id, height, posMs, item.files[0]?.id, videoCopy);
      transcodeSessionId = sess.session_id;
      transcodeToken = sess.token;
      attachHls(sess.playlist_url, posMs / 1000, wasPlaying);
    } catch (e) {
      error = e instanceof Error ? e.message : 'Transcode failed';
    }
  }

  function attachHls(playlistUrl: string, startSec: number, autoPlay: boolean) {
    // The HLS stream begins at t=0 representing content position startSec.
    // We track the offset ourselves; do NOT seek inside the stream.
    hlsOffsetSec = startSec;
    hlsActive = true;

    // Use file-level duration (from ffprobe) first, then fall back to item-level.
    const file = item?.files?.[0];
    const fileDurMs = file?.duration_ms ?? item?.duration_ms;
    if (fileDurMs) duration = fileDurMs / 1000;

    if (Hls.isSupported()) {
      hlsInstance = new Hls();
      hlsInstance.loadSource(playlistUrl);
      hlsInstance.attachMedia(videoEl);
      hlsInstance.on(Hls.Events.MANIFEST_PARSED, () => {
        if (autoPlay) videoEl.play().catch(() => {});
      });
    } else if (videoEl.canPlayType('application/vnd.apple.mpegURL')) {
      // Safari native HLS
      videoEl.src = playlistUrl;
      videoEl.load();
      const onMeta = () => {
        if (autoPlay) videoEl.play().catch(() => {});
        videoEl.removeEventListener('loadedmetadata', onMeta);
      };
      videoEl.addEventListener('loadedmetadata', onMeta);
    } else {
      error = 'HLS playback is not supported in this browser.';
    }
  }

  function destroyHls() {
    if (hlsInstance) {
      hlsInstance.destroy();
      hlsInstance = null;
    }
    hlsActive = false;
    hlsOffsetSec = 0;
  }

  async function stopTranscodeSession() {
    if (transcodeSessionId && transcodeToken) {
      try {
        await transcodeApi.stop(transcodeSessionId, transcodeToken);
      } catch (e) { console.warn(e); }
      transcodeSessionId = null;
      transcodeToken = null;
    }
  }

  // ── Video events ──────────────────────────────────────────────────────────────

  function onVideoLoaded() {
    if (!hlsActive) {
      // Direct play: videoEl.duration is authoritative.
      if (isFinite(videoEl.duration) && videoEl.duration > 0) {
        duration = videoEl.duration;
      }
      // Resume from last saved position (skip during quality switches).
      if (!skipAutoSeek && item && item.view_offset_ms > 0) {
        const offsetSec = item.view_offset_ms / 1000;
        if (duration - offsetSec > 30) {
          videoEl.currentTime = offsetSec;
        }
      }
      skipAutoSeek = false;
    }
    // HLS mode: duration is set once in attachHls from item.duration_ms.
    // Do NOT read videoEl.duration here — it grows as segments are produced.
    videoEl.play().catch(() => {});
  }

  function onTimeUpdate() {
    if (!seeking) currentTime = videoEl.currentTime + hlsOffsetSec;
    if (videoEl.buffered.length > 0 && duration > 0) {
      buffered = Math.min(1, (videoEl.buffered.end(videoEl.buffered.length - 1) + hlsOffsetSec) / duration);
    }
    // Direct play only: keep duration in sync with the video element.
    // In HLS mode, duration is fixed from item.duration_ms (set in attachHls).
    if (!hlsActive && isFinite(videoEl.duration) && videoEl.duration > 0) {
      duration = videoEl.duration;
    }
  }

  function onPlay()  { paused = false; ended = false; startProgressTimer(); }
  function onPause() { paused = true; stopProgressTimer(); saveProgress('paused'); }
  function onEnded() { ended = true; paused = true; stopProgressTimer(); saveProgress('stopped'); stopTranscodeSession(); }

  function startProgressTimer() {
    stopProgressTimer();
    progressTimer = setInterval(() => saveProgress('playing'), 5000);
  }
  function stopProgressTimer() {
    if (progressTimer) { clearInterval(progressTimer); progressTimer = null; }
  }
  function clearTimers() {
    stopProgressTimer();
    if (hideTimer) { clearTimeout(hideTimer); hideTimer = null; }
  }

  async function saveProgress(state: 'playing' | 'paused' | 'stopped') {
    if (!item || !videoEl || duration === 0) return;
    try {
      await itemApi.progress(item.id, Math.floor(currentTime * 1000), Math.floor(duration * 1000), state);
    } catch (e) { console.warn(e); }
  }

  function togglePlay() {
    if (!videoEl) return;
    if (videoEl.paused) videoEl.play().catch(() => {});
    else videoEl.pause();
  }

  function onKeyDown(e: KeyboardEvent) {
    if (!videoEl) return;
    // Close quality menu on Escape
    if (e.key === 'Escape') { showQualityMenu = false; return; }
    switch (e.key) {
      case ' ':
      case 'k':
        e.preventDefault();
        togglePlay();
        break;
      case 'ArrowRight':
        e.preventDefault();
        seekToContentTime(currentTime + 10);
        break;
      case 'ArrowLeft':
        e.preventDefault();
        seekToContentTime(currentTime - 10);
        break;
      case 'ArrowUp':
        e.preventDefault();
        videoEl.volume = Math.min(videoEl.volume + 0.1, 1);
        volume = videoEl.volume;
        break;
      case 'ArrowDown':
        e.preventDefault();
        videoEl.volume = Math.max(videoEl.volume - 0.1, 0);
        volume = videoEl.volume;
        break;
      case 'f':
        e.preventDefault();
        toggleFullscreen();
        break;
      case 'm':
        e.preventDefault();
        videoEl.muted = !videoEl.muted;
        muted = videoEl.muted;
        break;
    }
  }

  function getSeekFraction(e: MouseEvent | TouchEvent): number {
    const rect = seekBarEl.getBoundingClientRect();
    const clientX = 'touches' in e ? e.touches[0].clientX : e.clientX;
    return Math.max(0, Math.min(1, (clientX - rect.left) / rect.width));
  }

  function onSeekMouseDown(e: MouseEvent) {
    seeking = true;
    currentTime = getSeekFraction(e) * duration; // preview in content time
    window.addEventListener('mousemove', onSeekMouseMove);
    window.addEventListener('mouseup', onSeekMouseUp);
  }
  function onSeekMouseMove(e: MouseEvent) {
    if (!seeking) return;
    currentTime = getSeekFraction(e) * duration;
  }
  function onSeekMouseUp(e: MouseEvent) {
    if (!seeking) return;
    seeking = false;
    const targetSec = getSeekFraction(e) * duration;
    window.removeEventListener('mousemove', onSeekMouseMove);
    window.removeEventListener('mouseup', onSeekMouseUp);
    seekToContentTime(targetSec);
    saveProgress(videoEl.paused ? 'paused' : 'playing');
  }

  // Seek to an absolute content position (seconds).
  // In HLS mode, restarts the transcode session if the target is outside
  // the current stream window; otherwise seeks within the stream directly.
  function seekToContentTime(targetSec: number) {
    targetSec = Math.max(0, Math.min(targetSec, duration));
    if (!hlsActive) {
      videoEl.currentTime = targetSec;
      return;
    }
    const streamEnd = hlsOffsetSec + (isFinite(videoEl.duration) ? videoEl.duration : 0);
    if (targetSec >= hlsOffsetSec && targetSec <= streamEnd) {
      videoEl.currentTime = targetSec - hlsOffsetSec;
    } else {
      // Outside the buffered HLS window — restart transcode at new position.
      switchToTranscode(selectedQuality.height, Math.floor(targetSec * 1000));
    }
  }

  function onVolumeChange(e: Event) {
    volume = (e.target as HTMLInputElement).valueAsNumber;
    videoEl.volume = volume;
    if (volume > 0) videoEl.muted = false;
    muted = videoEl.muted;
  }

  async function toggleFullscreen() {
    if (!document.fullscreenElement) {
      await (containerEl.requestFullscreen || (containerEl as any).webkitRequestFullscreen)?.call(containerEl).catch(() => {});
      fullscreen = true;
    } else {
      await (document.exitFullscreen || (document as any).webkitExitFullscreen)?.call(document).catch(() => {});
      fullscreen = false;
    }
  }

  function onFullscreenChange() { fullscreen = !!(document.fullscreenElement || (document as any).webkitFullscreenElement); }

  function resetHideTimer() {
    showControls = true;
    if (hideTimer) clearTimeout(hideTimer);
    if (!paused) {
      hideTimer = setTimeout(() => {
        if (!showQualityMenu) showControls = false;
      }, 3000);
    }
  }

  function onMouseMove() { resetHideTimer(); }
  function onMouseLeave() {
    if (!paused && hideTimer) clearTimeout(hideTimer);
    if (!paused && !showQualityMenu) showControls = false;
  }

  function fmtTime(sec: number): string {
    if (!isFinite(sec)) return '0:00';
    const h = Math.floor(sec / 3600);
    const m = Math.floor((sec % 3600) / 60);
    const s = Math.floor(sec % 60);
    if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
    return `${m}:${String(s).padStart(2, '0')}`;
  }

  function goBack() {
    saveProgress('stopped');
    history.back();
  }

  $: progress = duration > 0 ? currentTime / duration : 0;
  $: showNextEpisodePrompt = nextEpisode != null && duration > 0 && (ended || duration - currentTime < 60);
  $: streamURL = item?.files?.[0]?.stream_url ?? '';
</script>

<svelte:head><title>{item?.title ?? 'Watch'} — OnScreen</title></svelte:head>

<svelte:window on:keydown={onKeyDown} on:fullscreenchange={onFullscreenChange} on:webkitfullscreenchange={onFullscreenChange} on:click={() => showQualityMenu = false} />

{#if !isDetailView}
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div
  class="player-container"
  class:hide-cursor={!showControls && !paused}
  bind:this={containerEl}
  on:mousemove={onMouseMove}
  on:mouseleave={onMouseLeave}
  role="region"
  aria-label="Video player"
>
  {#if loading}
    <div class="center-msg">
      <div class="spinner"></div>
    </div>
  {:else if error}
    <div class="center-msg">
      <p class="err-text">{error}</p>
      <button class="back-btn" on:click={goBack}>← Back</button>
    </div>
  {:else if item && streamURL}
    <!-- Video -->
    <video
      bind:this={videoEl}
      class="video"
      on:loadedmetadata={onVideoLoaded}
      on:timeupdate={onTimeUpdate}
      on:play={onPlay}
      on:pause={onPause}
      on:ended={onEnded}
      preload="metadata"
      autoplay
    >
      <track kind="captions" />
    </video>

    <!-- Fanart background (blurred, behind controls) -->
    {#if item.fanart_path}
      <div class="fanart-bg" style="background-image:url('/artwork/{item.fanart_path}?v={item.updated_at}')"></div>
    {/if}

    <!-- Controls overlay -->
    <!-- svelte-ignore a11y-click-events-have-key-events -->
    <!-- svelte-ignore a11y-no-static-element-interactions -->
    <div class="controls-overlay" class:visible={showControls || paused} on:click={togglePlay}>

      <!-- Top bar -->
      <div class="top-bar" on:click|stopPropagation>
        <button class="back-btn icon-btn" on:click={goBack} title="Back">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" width="20" height="20">
            <polyline points="15 18 9 12 15 6"/>
          </svg>
        </button>
        <div class="top-title">
          <span class="top-title-main">{item.title}</span>
          {#if item.type === 'episode' && item.index != null}
            <span class="top-title-sub">Episode {item.index}</span>
          {/if}
        </div>
      </div>

      <!-- Bottom bar -->
      <div class="bottom-bar" on:click|stopPropagation>
        <!-- Seek bar -->
        <div
          class="seek-bar"
          bind:this={seekBarEl}
          on:mousedown={onSeekMouseDown}
          role="slider"
          aria-label="Seek"
          aria-valuemin={0}
          aria-valuemax={duration}
          aria-valuenow={currentTime}
          tabindex="0"
        >
          <div class="seek-track">
            <div class="seek-buffered" style="width:{buffered * 100}%"></div>
            <div class="seek-progress" style="width:{progress * 100}%"></div>
            <div class="seek-thumb" style="left:{progress * 100}%"></div>
          </div>
        </div>

        <!-- Controls row -->
        <div class="controls-row">
          <div class="controls-left">
            <!-- Play/pause -->
            <button class="icon-btn" on:click={togglePlay} title={paused ? 'Play (k)' : 'Pause (k)'}>
              {#if paused}
                <svg viewBox="0 0 24 24" fill="currentColor" width="22" height="22">
                  <polygon points="5,3 19,12 5,21"/>
                </svg>
              {:else}
                <svg viewBox="0 0 24 24" fill="currentColor" width="22" height="22">
                  <rect x="6" y="4" width="4" height="16"/><rect x="14" y="4" width="4" height="16"/>
                </svg>
              {/if}
            </button>

            <!-- Skip back 10s -->
            <button class="icon-btn small" on:click={() => seekToContentTime(currentTime - 10)} title="−10s (←)">
              <svg viewBox="0 0 24 24" fill="currentColor" width="18" height="18">
                <path d="M12.5 3a9 9 0 1 0 9 9h-2a7 7 0 1 1-7-7V3z"/>
                <path d="M12.5 1 8 5l4.5 4V1z"/>
                <text x="12" y="14.5" text-anchor="middle" font-size="5" font-family="sans-serif" fill="currentColor">10</text>
              </svg>
            </button>

            <!-- Skip forward 10s -->
            <button class="icon-btn small" on:click={() => seekToContentTime(currentTime + 10)} title="+10s (→)">
              <svg viewBox="0 0 24 24" fill="currentColor" width="18" height="18">
                <path d="M11.5 3a9 9 0 1 1-9 9h2a7 7 0 1 0 7-7V3z"/>
                <path d="M11.5 1l4.5 4-4.5 4V1z"/>
                <text x="12" y="14.5" text-anchor="middle" font-size="5" font-family="sans-serif" fill="currentColor">10</text>
              </svg>
            </button>

            <!-- Volume -->
            <button class="icon-btn small" on:click={() => { videoEl.muted = !videoEl.muted; muted = videoEl.muted; }} title="Mute (m)">
              {#if muted || volume === 0}
                <svg viewBox="0 0 24 24" fill="currentColor" width="18" height="18">
                  <path d="M11 5L6 9H2v6h4l5 4V5zM23 9l-6 6M17 9l6 6" stroke="currentColor" stroke-width="2" fill="none"/>
                </svg>
              {:else}
                <svg viewBox="0 0 24 24" fill="currentColor" width="18" height="18">
                  <path d="M11 5L6 9H2v6h4l5 4V5z"/>
                  <path d="M15.54 8.46a5 5 0 0 1 0 7.07M19.07 4.93a10 10 0 0 1 0 14.14" stroke="currentColor" stroke-width="2" fill="none"/>
                </svg>
              {/if}
            </button>
            <input
              class="volume-slider"
              type="range"
              min="0"
              max="1"
              step="0.05"
              value={muted ? 0 : volume}
              on:input={onVolumeChange}
              aria-label="Volume"
            />

            <!-- Time -->
            <span class="time">{fmtTime(currentTime)} / {fmtTime(duration)}</span>
          </div>

          <div class="controls-right">
            <!-- Quality picker -->
            <div class="quality-picker" on:click|stopPropagation>
              <button
                class="icon-btn small quality-btn"
                on:click|stopPropagation={() => showQualityMenu = !showQualityMenu}
                title="Quality"
              >
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="16" height="16">
                  <path d="M12 20h9M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/>
                </svg>
                <span class="quality-label">{selectedQuality.label === 'Auto (Direct Play)' ? 'Auto' : selectedQuality.label}</span>
              </button>

              {#if showQualityMenu}
                <!-- svelte-ignore a11y-no-static-element-interactions -->
                <div
                  class="quality-menu"
                  on:click|stopPropagation
                  role="menu"
                  aria-label="Quality options"
                >
                  {#each availableQualities as q}
                    <button
                      class="quality-option"
                      class:active={q === selectedQuality}
                      on:click={() => selectQuality(q)}
                      role="menuitem"
                    >
                      {q.label}
                    </button>
                  {/each}
                </div>
              {/if}
            </div>

            <!-- Fullscreen -->
            <button class="icon-btn small" on:click={toggleFullscreen} title="Fullscreen (f)">
              {#if fullscreen}
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="18" height="18">
                  <path d="M8 3v3a2 2 0 0 1-2 2H3m18 0h-3a2 2 0 0 1-2-2V3m0 18v-3a2 2 0 0 1 2-2h3M3 16h3a2 2 0 0 1 2 2v3"/>
                </svg>
              {:else}
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="18" height="18">
                  <path d="M8 3H5a2 2 0 0 0-2 2v3m18 0V5a2 2 0 0 0-2-2h-3m0 18h3a2 2 0 0 0 2-2v-3M3 16v3a2 2 0 0 0 2 2h3"/>
                </svg>
              {/if}
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Next episode prompt -->
    {#if showNextEpisodePrompt && nextEpisode}
      <div class="next-episode">
        <span class="next-label">Up Next</span>
        <span class="next-title">{nextEpisode.title}</span>
        <a href="/watch/{nextEpisode.id}" class="next-btn">
          Play →
        </a>
      </div>
    {/if}

  {:else if item}
    <div class="center-msg">
      <p class="err-text">No playable file found for this item.</p>
      <button class="back-btn" on:click={goBack}>← Back</button>
    </div>
  {/if}
</div>
{/if}

{#if isDetailView && item}
<!-- Show / Season detail view -->
<div class="detail-page">
  <!-- Fanart hero -->
  {#if item.fanart_path}
    <div class="detail-hero" style="background-image:url('/artwork/{item.fanart_path}?v={item.updated_at}')">
      <div class="detail-hero-fade"></div>
    </div>
  {/if}

  <div class="detail-content">
    <button class="detail-back" on:click={() => history.back()}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" width="18" height="18"><polyline points="15 18 9 12 15 6"/></svg>
      Back
    </button>

    <div class="detail-header">
      {#if item.poster_path}
        <img class="detail-poster" src="/artwork/{item.poster_path}?v={item.updated_at}" alt="{item.title}" />
      {/if}
      <div class="detail-meta">
        <h1 class="detail-title">{item.title}</h1>
        <div class="detail-tags">
          {#if item.year}<span>{item.year}</span>{/if}
          {#if item.content_rating}<span>{item.content_rating}</span>{/if}
          {#if item.rating}<span>&#9733; {item.rating.toFixed(1)}</span>{/if}
        </div>
        {#if item.genres?.length}
          <div class="detail-genres">{item.genres.join(', ')}</div>
        {/if}
        {#if item.summary}
          <p class="detail-summary">{item.summary}</p>
        {/if}
      </div>
    </div>

    <!-- Fix Match button (shows and movies) -->
    {#if item.type === 'show' || item.type === 'movie'}
      <button class="fix-match-btn" on:click={openMatchModal}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
        Fix Match
      </button>
    {/if}

    <!-- Season selector (shows only) -->
    {#if item.type === 'show' && seasons.length > 1}
      {#if seasons.length <= 9}
        <div class="season-tabs">
          {#each seasons as season}
            <button
              class="season-tab"
              class:active={selectedSeasonId === season.id}
              on:click={() => selectSeason(season.id)}
            >
              {season.title}
            </button>
          {/each}
        </div>
      {:else}
        <div class="season-dropdown">
          <select
            value={selectedSeasonId}
            on:change={(e) => selectSeason(e.currentTarget.value)}
          >
            {#each seasons as season}
              <option value={season.id}>{season.title}</option>
            {/each}
          </select>
        </div>
      {/if}
    {/if}

    <!-- Episode list -->
    <div class="episode-list">
      {#each selectedEpisodes as ep}
        <a href="/watch/{ep.id}" class="episode-row">
          <div class="ep-number">{ep.index ?? '—'}</div>
          <div class="ep-info">
            <div class="ep-title">{ep.title}</div>
            {#if ep.summary}
              <div class="ep-summary">{ep.summary}</div>
            {/if}
          </div>
          {#if ep.duration_ms}
            <div class="ep-duration">{Math.round(ep.duration_ms / 60000)}m</div>
          {/if}
        </a>
      {/each}
      {#if selectedEpisodes.length === 0}
        <div class="ep-empty">No episodes found.</div>
      {/if}
    </div>
  </div>
</div>
{/if}

<!-- Fix Match modal -->
{#if showMatchModal}
<!-- svelte-ignore a11y-click-events-have-key-events -->
<!-- svelte-ignore a11y-no-static-element-interactions -->
<div class="match-overlay" on:click={() => showMatchModal = false}>
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <!-- svelte-ignore a11y-no-static-element-interactions -->
  <div class="match-modal" on:click|stopPropagation>
    <h2>Fix Match</h2>
    <p class="match-hint">Search TMDB for the correct title and select it.</p>

    <form class="match-search-form" on:submit|preventDefault={searchMatch}>
      <input
        type="text"
        bind:value={matchQuery}
        placeholder="Search TMDB..."
        class="match-input"
        autofocus
      />
      <button type="submit" class="match-search-btn" disabled={matchSearching}>
        {matchSearching ? 'Searching...' : 'Search'}
      </button>
    </form>

    {#if matchError}
      <div class="match-error">{matchError}</div>
    {/if}

    <div class="match-results">
      {#each matchCandidates as c}
        <button
          class="match-result"
          disabled={matchApplying}
          on:click={() => applyMatch(c.tmdb_id)}
        >
          {#if c.poster_url}
            <img class="match-poster" src={c.poster_url} alt="" />
          {:else}
            <div class="match-poster-blank"></div>
          {/if}
          <div class="match-result-info">
            <div class="match-result-title">{c.title}</div>
            <div class="match-result-meta">
              {#if c.year}<span>{c.year}</span>{/if}
              {#if c.rating}<span>&#9733; {c.rating.toFixed(1)}</span>{/if}
            </div>
            {#if c.summary}
              <div class="match-result-summary">{c.summary}</div>
            {/if}
          </div>
        </button>
      {/each}
      {#if !matchSearching && matchCandidates.length === 0 && matchQuery}
        <div class="match-empty">No results found.</div>
      {/if}
    </div>

    <button class="match-cancel" on:click={() => showMatchModal = false}>Cancel</button>
  </div>
</div>
{/if}

<style>
  .player-container {
    position: fixed;
    inset: 0;
    background: #000;
    display: flex;
    align-items: center;
    justify-content: center;
    overflow: hidden;
  }
  .player-container.hide-cursor { cursor: none; }

  .video {
    width: 100%;
    height: 100%;
    object-fit: contain;
    display: block;
    position: relative;
    z-index: 1;
  }

  .fanart-bg {
    position: absolute;
    inset: 0;
    background-size: cover;
    background-position: center;
    filter: blur(40px) brightness(0.2);
    transform: scale(1.1);
    z-index: 0;
    pointer-events: none;
  }

  /* ── Controls overlay ─────────────────────────────────── */
  .controls-overlay {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    opacity: 0;
    transition: opacity 0.25s;
    z-index: 10;
  }
  .controls-overlay.visible { opacity: 1; }

  .top-bar {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 1.25rem 1.5rem;
    background: linear-gradient(to bottom, rgba(0,0,0,0.7) 0%, transparent 100%);
  }

  .top-title {
    display: flex;
    flex-direction: column;
  }
  .top-title-main {
    font-size: 0.95rem;
    font-weight: 600;
    color: #fff;
    line-height: 1.2;
  }
  .top-title-sub {
    font-size: 0.75rem;
    color: rgba(255,255,255,0.55);
  }

  .bottom-bar {
    padding: 0 1.5rem 1.25rem;
    background: linear-gradient(to top, rgba(0,0,0,0.8) 0%, transparent 100%);
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  /* ── Seek bar ─────────────────────────────────────────── */
  .seek-bar {
    width: 100%;
    height: 18px;
    cursor: pointer;
    display: flex;
    align-items: center;
    padding: 6px 0;
    box-sizing: border-box;
  }
  .seek-track {
    position: relative;
    width: 100%;
    height: 4px;
    background: rgba(255,255,255,0.2);
    border-radius: 2px;
    overflow: visible;
  }
  .seek-buffered {
    position: absolute;
    top: 0; left: 0;
    height: 100%;
    background: rgba(255,255,255,0.3);
    border-radius: 2px;
    pointer-events: none;
  }
  .seek-progress {
    position: absolute;
    top: 0; left: 0;
    height: 100%;
    background: #7c6af7;
    border-radius: 2px;
    pointer-events: none;
  }
  .seek-thumb {
    position: absolute;
    top: 50%;
    transform: translate(-50%, -50%);
    width: 14px;
    height: 14px;
    background: #fff;
    border-radius: 50%;
    pointer-events: none;
    box-shadow: 0 1px 4px rgba(0,0,0,0.5);
    transition: transform 0.1s;
  }
  .seek-bar:hover .seek-thumb { transform: translate(-50%, -50%) scale(1.3); }
  .seek-bar:hover .seek-track { height: 5px; }

  /* ── Controls row ─────────────────────────────────────── */
  .controls-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .controls-left, .controls-right {
    display: flex;
    align-items: center;
    gap: 0.25rem;
  }

  .icon-btn {
    background: none;
    border: none;
    color: rgba(255,255,255,0.9);
    cursor: pointer;
    padding: 0.4rem;
    border-radius: 6px;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: background 0.12s, color 0.12s;
  }
  .icon-btn:hover { background: rgba(255,255,255,0.1); color: #fff; }
  .icon-btn.small { padding: 0.3rem; }

  .back-btn {
    background: none;
    border: none;
    color: rgba(255,255,255,0.85);
    cursor: pointer;
    padding: 0.35rem 0.5rem;
    border-radius: 6px;
    display: flex;
    align-items: center;
    gap: 0.25rem;
    transition: background 0.12s;
  }
  .back-btn:hover { background: rgba(255,255,255,0.1); color: #fff; }

  .volume-slider {
    width: 70px;
    height: 4px;
    -webkit-appearance: none;
    appearance: none;
    background: rgba(255,255,255,0.3);
    border-radius: 2px;
    cursor: pointer;
    outline: none;
  }
  .volume-slider::-webkit-slider-thumb {
    -webkit-appearance: none;
    width: 12px; height: 12px;
    border-radius: 50%;
    background: #fff;
    cursor: pointer;
  }

  .time {
    font-size: 0.78rem;
    color: rgba(255,255,255,0.75);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
    padding-left: 0.25rem;
  }

  /* ── Quality picker ───────────────────────────────────── */
  .quality-picker {
    position: relative;
  }

  .quality-btn {
    gap: 0.3rem;
    font-size: 0.72rem;
    font-weight: 600;
    color: rgba(255,255,255,0.85);
  }

  .quality-label {
    font-size: 0.72rem;
    font-weight: 600;
    letter-spacing: 0.01em;
  }

  .quality-menu {
    position: absolute;
    bottom: calc(100% + 8px);
    right: 0;
    background: rgba(15,15,25,0.95);
    border: 1px solid rgba(255,255,255,0.12);
    border-radius: 8px;
    padding: 0.35rem;
    display: flex;
    flex-direction: column;
    min-width: 160px;
    z-index: 30;
    backdrop-filter: blur(12px);
    box-shadow: 0 4px 24px rgba(0,0,0,0.5);
  }

  .quality-option {
    background: none;
    border: none;
    color: rgba(255,255,255,0.8);
    cursor: pointer;
    padding: 0.45rem 0.75rem;
    border-radius: 5px;
    text-align: left;
    font-size: 0.8rem;
    transition: background 0.1s, color 0.1s;
    white-space: nowrap;
  }
  .quality-option:hover { background: rgba(255,255,255,0.1); color: #fff; }
  .quality-option.active { color: #7c6af7; font-weight: 600; }

  /* ── Next episode ─────────────────────────────────────── */
  .next-episode {
    position: absolute;
    bottom: 5rem;
    right: 1.5rem;
    background: rgba(15,15,25,0.9);
    border: 1px solid rgba(255,255,255,0.1);
    border-radius: 10px;
    padding: 0.75rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    z-index: 20;
    backdrop-filter: blur(8px);
  }
  .next-label {
    font-size: 0.65rem;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: #7c6af7;
    font-weight: 700;
  }
  .next-title {
    font-size: 0.8rem;
    color: #eeeef8;
    font-weight: 500;
    max-width: 200px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .next-btn {
    align-self: flex-end;
    padding: 0.3rem 0.7rem;
    background: #7c6af7;
    border-radius: 6px;
    color: #fff;
    font-size: 0.75rem;
    font-weight: 600;
    text-decoration: none;
    transition: background 0.12s;
  }
  .next-btn:hover { background: #8f7ef9; }

  /* ── Loading / error ─────────────────────────────────── */
  .center-msg {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 1rem;
    color: #eeeef8;
  }
  .err-text { font-size: 0.9rem; color: #fca5a5; margin: 0; }

  .spinner {
    width: 36px; height: 36px;
    border: 3px solid rgba(255,255,255,0.15);
    border-top-color: #7c6af7;
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }

  /* ── Detail view (shows / seasons) ───────────────── */
  .detail-page {
    position: fixed; inset: 0;
    background: #0a0a10;
    overflow-y: auto;
    color: #eeeef8;
  }
  .detail-hero {
    position: relative;
    width: 100%; height: 340px;
    background-size: cover;
    background-position: center top;
  }
  .detail-hero-fade {
    position: absolute; inset: 0;
    background: linear-gradient(to bottom, transparent 30%, #0a0a10 100%);
  }
  .detail-content {
    position: relative;
    max-width: 900px;
    margin: -100px auto 0;
    padding: 0 2rem 3rem;
    z-index: 1;
  }
  .detail-back {
    display: inline-flex; align-items: center; gap: 0.35rem;
    background: none; border: none;
    color: #888; font-size: 0.8rem;
    cursor: pointer; margin-bottom: 1.25rem;
    padding: 0;
  }
  .detail-back:hover { color: #ccc; }

  .detail-header {
    display: flex; gap: 1.5rem;
    margin-bottom: 2rem;
  }
  .detail-poster {
    width: 160px; height: auto;
    border-radius: 8px;
    object-fit: cover;
    flex-shrink: 0;
    box-shadow: 0 4px 24px rgba(0,0,0,0.5);
  }
  .detail-meta { flex: 1; min-width: 0; }
  .detail-title {
    font-size: 1.6rem; font-weight: 700;
    letter-spacing: -0.02em;
    margin: 0 0 0.5rem;
  }
  .detail-tags {
    display: flex; gap: 0.75rem;
    font-size: 0.8rem; color: #666;
    margin-bottom: 0.4rem;
  }
  .detail-genres { font-size: 0.78rem; color: #555; margin-bottom: 0.75rem; }
  .detail-summary {
    font-size: 0.82rem; color: #888;
    line-height: 1.6; margin: 0;
    display: -webkit-box;
    -webkit-line-clamp: 4;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  /* Season tabs */
  .season-tabs {
    display: flex; gap: 0.25rem;
    margin-bottom: 1.25rem;
    overflow-x: auto;
    scrollbar-width: none;
  }
  .season-tabs::-webkit-scrollbar { display: none; }
  .season-tab {
    padding: 0.4rem 0.9rem;
    background: rgba(255,255,255,0.04);
    border: 1px solid rgba(255,255,255,0.06);
    border-radius: 6px;
    color: #777; font-size: 0.78rem; font-weight: 500;
    cursor: pointer; white-space: nowrap;
    transition: all 0.12s;
  }
  .season-tab:hover { color: #bbb; border-color: rgba(255,255,255,0.12); }
  .season-tab.active {
    background: rgba(124,106,247,0.12);
    border-color: rgba(124,106,247,0.3);
    color: #a89ffa;
  }
  .season-dropdown {
    margin-bottom: 1.25rem;
  }
  .season-dropdown select {
    background: rgba(255,255,255,0.04);
    border: 1px solid rgba(255,255,255,0.1);
    border-radius: 6px;
    color: #ccc;
    font-size: 0.82rem;
    font-weight: 500;
    padding: 0.45rem 2rem 0.45rem 0.75rem;
    cursor: pointer;
    appearance: none;
    background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' fill='%23777' viewBox='0 0 16 16'%3E%3Cpath d='M8 11L3 6h10z'/%3E%3C/svg%3E");
    background-repeat: no-repeat;
    background-position: right 0.6rem center;
  }
  .season-dropdown select:focus {
    outline: none;
    border-color: rgba(124,106,247,0.4);
    box-shadow: 0 0 0 3px rgba(124,106,247,0.1);
  }
  .season-dropdown select option {
    background: #16161f;
    color: #ccc;
  }

  /* Episode list */
  .episode-list {
    display: flex; flex-direction: column;
  }
  .episode-row {
    display: flex; align-items: flex-start; gap: 1rem;
    padding: 0.85rem 0.5rem;
    border-bottom: 1px solid rgba(255,255,255,0.04);
    text-decoration: none; color: inherit;
    transition: background 0.1s;
  }
  .episode-row:hover { background: rgba(255,255,255,0.03); }
  .ep-number {
    width: 2rem; flex-shrink: 0;
    font-size: 0.85rem; font-weight: 600;
    color: #444; text-align: center;
    padding-top: 0.1rem;
  }
  .ep-info { flex: 1; min-width: 0; }
  .ep-title {
    font-size: 0.88rem; font-weight: 500;
    color: #ddd; margin-bottom: 0.2rem;
  }
  .ep-summary {
    font-size: 0.75rem; color: #555;
    line-height: 1.5;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .ep-duration {
    font-size: 0.75rem; color: #444;
    flex-shrink: 0; padding-top: 0.15rem;
  }
  .ep-empty {
    padding: 2rem; text-align: center;
    font-size: 0.85rem; color: #444;
  }

  /* Fix Match button */
  .fix-match-btn {
    display: inline-flex; align-items: center; gap: 0.35rem;
    background: rgba(255,255,255,0.04);
    border: 1px solid rgba(255,255,255,0.08);
    border-radius: 6px;
    color: #777; font-size: 0.75rem; font-weight: 500;
    cursor: pointer; padding: 0.35rem 0.7rem;
    margin-bottom: 1.5rem;
    transition: all 0.12s;
  }
  .fix-match-btn:hover { color: #bbb; border-color: rgba(255,255,255,0.15); background: rgba(255,255,255,0.06); }

  /* Match modal overlay */
  .match-overlay {
    position: fixed; inset: 0;
    background: rgba(0,0,0,0.7);
    z-index: 100;
    display: flex; align-items: center; justify-content: center;
    padding: 1rem;
  }
  .match-modal {
    background: #13131a;
    border: 1px solid rgba(255,255,255,0.08);
    border-radius: 12px;
    width: 100%; max-width: 520px; max-height: 80vh;
    display: flex; flex-direction: column;
    padding: 1.5rem;
    overflow: hidden;
  }
  .match-modal h2 {
    font-size: 1.1rem; font-weight: 700; color: #eeeef8;
    margin: 0 0 0.25rem;
  }
  .match-hint {
    font-size: 0.75rem; color: #555; margin: 0 0 1rem;
  }
  .match-search-form {
    display: flex; gap: 0.5rem; margin-bottom: 0.75rem;
  }
  .match-input {
    flex: 1;
    background: rgba(255,255,255,0.04);
    border: 1px solid rgba(255,255,255,0.09);
    border-radius: 7px;
    padding: 0.45rem 0.7rem;
    font-size: 0.85rem; color: #eeeef8;
    outline: none;
  }
  .match-input:focus { border-color: #7c6af7; box-shadow: 0 0 0 3px rgba(124,106,247,0.12); }
  .match-search-btn {
    padding: 0.45rem 0.8rem;
    background: #7c6af7; border: none; border-radius: 7px;
    color: #fff; font-size: 0.8rem; font-weight: 600;
    cursor: pointer; white-space: nowrap;
  }
  .match-search-btn:hover { background: #8f7ef9; }
  .match-search-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .match-error {
    font-size: 0.78rem; color: #fca5a5;
    padding: 0.4rem 0; margin-bottom: 0.5rem;
  }
  .match-results {
    flex: 1; overflow-y: auto; min-height: 0;
    display: flex; flex-direction: column; gap: 0.25rem;
    margin-bottom: 1rem;
  }
  .match-result {
    display: flex; align-items: flex-start; gap: 0.75rem;
    padding: 0.65rem 0.5rem;
    background: none; border: 1px solid transparent;
    border-radius: 8px; cursor: pointer;
    text-align: left; color: inherit;
    transition: all 0.1s;
  }
  .match-result:hover { background: rgba(255,255,255,0.03); border-color: rgba(255,255,255,0.06); }
  .match-result:disabled { opacity: 0.5; cursor: wait; }
  .match-poster {
    width: 48px; height: 72px; object-fit: cover;
    border-radius: 4px; flex-shrink: 0;
    background: #1a1a24;
  }
  .match-poster-blank {
    width: 48px; height: 72px;
    border-radius: 4px; flex-shrink: 0;
    background: #1a1a24;
  }
  .match-result-info { flex: 1; min-width: 0; }
  .match-result-title { font-size: 0.85rem; font-weight: 500; color: #ddd; }
  .match-result-meta {
    display: flex; gap: 0.5rem;
    font-size: 0.75rem; color: #666;
    margin-top: 0.15rem;
  }
  .match-result-summary {
    font-size: 0.72rem; color: #555; line-height: 1.5;
    margin-top: 0.3rem;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .match-empty {
    padding: 1.5rem; text-align: center;
    font-size: 0.8rem; color: #444;
  }
  .match-cancel {
    align-self: flex-end;
    background: none; border: 1px solid rgba(255,255,255,0.08);
    border-radius: 6px; padding: 0.35rem 0.8rem;
    color: #777; font-size: 0.78rem; cursor: pointer;
  }
  .match-cancel:hover { color: #bbb; border-color: rgba(255,255,255,0.15); }
</style>
