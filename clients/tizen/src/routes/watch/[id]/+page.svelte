<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import {
    api,
    endpoints,
    Unauthorized,
    type ChildItem,
    type ItemDetail,
    type Chapter,
    type Marker,
    type NotificationEvent
  } from '$lib/api';
  import { focusManager } from '$lib/focus/manager';
  import type { RemoteKey } from '$lib/focus/keys';
  import { avplay } from '$lib/player/avplay';
  import { ProgressReporter } from '$lib/player/progress-reporter';

  const itemID = page.params.id!;
  // Fallback HTML5 video element — used only when AVPlay isn't
  // available (i.e., `vite dev` against a desktop browser). On real
  // Tizen hardware AVPlay renders to a hardware overlay behind the
  // webview; the <video> element stays unused.
  let video: HTMLVideoElement | undefined = $state();

  let item = $state<ItemDetail | null>(null);
  let error = $state('');
  let loading = $state(true);
  let paused = $state(true);
  let position = $state(0);
  let duration = $state(0);
  let controlsVisible = $state(true);
  let controlsTimer: ReturnType<typeof setTimeout> | null = null;

  let session: { session_id: string; token: string; playlist_url: string } | null = null;
  let reporter: ProgressReporter | null = null;
  let usingAvPlay = false;

  // Chapters: surface as jump targets. Start offsets used for green-button cycling.
  const chapters = $derived<Chapter[]>(item?.files[0]?.chapters ?? []);

  // Intro / credits markers fetched alongside the item — drives the
  // Skip Intro / Skip Credits overlay. Empty list for non-episode
  // types and shows without auto-detected markers; the overlay
  // never renders in those cases.
  let markers = $state<Marker[]>([]);
  let activeMarker = $state<Marker | null>(null);
  // Per-marker dismissal so a skipped marker doesn't re-pop when
  // the user scrubs back across it.
  const dismissedMarkers = new Set<number>();

  // Up Next: chronologically-next sibling (next episode of the
  // same season, next track on the same album, next chapter of
  // the same audiobook). 25 s lead-in for episodes / podcasts;
  // music + audiobook chapters chain silently at EOS instead so
  // the closing seconds aren't clipped.
  let nextSibling = $state<ChildItem | null>(null);
  let upNextShown = $state(false);
  let upNextCountdown = $state(10);
  let upNextTimer: ReturnType<typeof setInterval> | null = null;

  // Cross-device resume sync via SSE. When the same user reports
  // new progress on another device while this player is paused,
  // snap to that position so resume picks up where the other
  // device left off. Active local playback wins.
  let syncEventSource: EventSource | null = null;
  let lastReportedPositionMs = -1;

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
    const target = Math.max(0, Math.min(duration, position + deltaMs));
    if (usingAvPlay) {
      avplay.seekTo(target);
    } else if (video) {
      video.currentTime = target / 1000;
    }
    showControls();
  }

  function togglePlay() {
    if (usingAvPlay) {
      if (paused) avplay.resume();
      else avplay.pause();
      paused = !paused;
    } else if (video) {
      if (video.paused) void video.play();
      else video.pause();
    }
    showControls();
  }

  function jumpToChapter(dir: 1 | -1) {
    if (chapters.length === 0) return;
    const idx = chapters.findIndex((c) => c.start_ms > position + 2000 * dir);
    let target = dir === 1 ? idx : idx === -1 ? chapters.length - 1 : Math.max(0, idx - 1);
    if (target < 0) target = 0;
    const ch = chapters[target];
    if (!ch) return;
    if (usingAvPlay) avplay.seekTo(ch.start_ms);
    else if (video) video.currentTime = ch.start_ms / 1000;
    showControls();
  }

  async function stopAndLeave() {
    if (reporter) reporter.stopped(position, duration);
    if (session && api.getToken()) {
      void endpoints.transcode.stop(session.session_id, session.token).catch(() => {});
    }
    if (usingAvPlay) avplay.close();
    goto(`/item/${itemID}`);
  }

  function onKey(k: RemoteKey): boolean {
    // Up Next overlay grabs OK / Back when visible — Enter chains
    // immediately, Back dismisses for the rest of this play.
    if (upNextShown) {
      if (k === 'enter' && nextSibling) { goToNext(nextSibling); return true; }
      if (k === 'back') { dismissUpNext(); return true; }
    }
    // Skip Intro / Skip Credits overlay handles Enter when visible
    // so the user doesn't have to find a button.
    if (activeMarker) {
      if (k === 'enter') { skipMarker(); return true; }
      if (k === 'back') { dismissMarker(); return true; }
    }
    switch (k) {
      case 'back':
        void stopAndLeave();
        return true;
      case 'enter':
      case 'playpause':
        togglePlay();
        return true;
      case 'play':
        if (paused) {
          if (usingAvPlay) avplay.resume();
          else void video?.play();
          paused = false;
        }
        return true;
      case 'pause':
        if (!paused) {
          if (usingAvPlay) avplay.pause();
          else video?.pause();
          paused = true;
        }
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

  // ── Markers ──────────────────────────────────────────────────────

  async function loadMarkers() {
    try {
      markers = await endpoints.items.markers(itemID);
    } catch {
      markers = [];
    }
  }

  function updateActiveMarker() {
    if (markers.length === 0) {
      if (activeMarker) activeMarker = null;
      return;
    }
    const within = markers.find(
      (m) => position >= m.start_ms && position < m.end_ms && !dismissedMarkers.has(m.start_ms)
    );
    activeMarker = within ?? null;
  }

  function skipMarker() {
    const m = activeMarker;
    if (!m) return;
    dismissedMarkers.add(m.start_ms);
    if (usingAvPlay) avplay.seekTo(m.end_ms);
    else if (video) video.currentTime = m.end_ms / 1000;
    activeMarker = null;
    showControls();
  }

  function dismissMarker() {
    if (!activeMarker) return;
    dismissedMarkers.add(activeMarker.start_ms);
    activeMarker = null;
  }

  // ── Up Next ──────────────────────────────────────────────────────

  // Match the Android PlaybackFragment defaults so behaviour is
  // consistent across clients.
  const UP_NEXT_LEAD_MS = 25_000;
  const UP_NEXT_COUNTDOWN_SEC = 10;

  async function loadNextSibling() {
    if (!item || !item.parent_id || item.index == null) return;
    if (
      item.type !== 'episode' &&
      item.type !== 'track' &&
      item.type !== 'audiobook_chapter' &&
      item.type !== 'podcast_episode'
    ) {
      return;
    }
    try {
      const kids = await endpoints.items.children(item.parent_id);
      const target = kids
        .filter((k) => k.type === item!.type && k.index != null)
        .sort((a, b) => (a.index ?? 0) - (b.index ?? 0))
        .find((k) => (k.index ?? -1) === (item!.index ?? -1) + 1);
      if (target) nextSibling = target;
    } catch {
      // Best-effort.
    }
  }

  function maybeShowUpNext() {
    if (!nextSibling || upNextShown || duration <= 0) return;
    // Music + audiobook chapters chain silently at EOS — overlay
    // would clip the outro / closing line.
    if (item?.type === 'track' || item?.type === 'audiobook_chapter') return;
    if (duration - position > UP_NEXT_LEAD_MS) return;

    upNextShown = true;
    upNextCountdown = UP_NEXT_COUNTDOWN_SEC;
    if (upNextTimer) clearInterval(upNextTimer);
    upNextTimer = setInterval(() => {
      upNextCountdown -= 1;
      if (upNextCountdown <= 0 && nextSibling) goToNext(nextSibling);
    }, 1000);
  }

  function dismissUpNext() {
    upNextShown = false;
    if (upNextTimer) {
      clearInterval(upNextTimer);
      upNextTimer = null;
    }
  }

  function goToNext(target: ChildItem) {
    if (upNextTimer) {
      clearInterval(upNextTimer);
      upNextTimer = null;
    }
    reporter?.stopped(position, duration);
    if (session && api.getToken()) {
      void endpoints.transcode.stop(session.session_id, session.token).catch(() => {});
    }
    if (usingAvPlay) avplay.close();
    goto(`/watch/${target.id}`);
  }

  // ── Cross-device sync ────────────────────────────────────────────

  function startSyncStream() {
    const origin = api.getOrigin();
    const tok = api.getToken();
    if (!origin || !tok) return;
    try {
      syncEventSource = new EventSource(
        `${origin}/api/v1/notifications/stream?token=${encodeURIComponent(tok)}`
      );
      syncEventSource.onmessage = onSyncEvent;
    } catch {
      // EventSource construction rarely throws; treat any failure
      // as "no sync today" and keep playing.
    }
  }

  function onSyncEvent(ev: MessageEvent) {
    let data: NotificationEvent;
    try {
      data = JSON.parse(ev.data) as NotificationEvent;
    } catch {
      return;
    }
    if (data.type !== 'progress.updated' || data.item_id !== itemID) return;
    if (!data.data?.position_ms) return;
    if (!paused) return; // active local playback wins
    const newPos = data.data.position_ms;
    if (lastReportedPositionMs >= 0 && Math.abs(newPos - lastReportedPositionMs) < 2000) {
      return;
    }
    if (usingAvPlay) avplay.seekTo(newPos);
    else if (video) video.currentTime = newPos / 1000;
  }

  function stopSyncStream() {
    syncEventSource?.close();
    syncEventSource = null;
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

        // Markers + next-sibling load alongside the playback
        // session — neither blocks the start. Failures are
        // best-effort (empty marker list = no Skip button; no
        // next sibling = natural EOS exit).
        void loadMarkers();
        void loadNextSibling();
        startSyncStream();

        const file = item.files[0];
        const startMs = item.view_offset_ms ?? 0;

        session = await endpoints.transcode.start({
          itemId: itemID,
          height: 1080,
          positionMs: startMs,
          fileId: file.id,
          supportsHEVC: true
        });

        const fullURL = session.playlist_url.startsWith('http')
          ? session.playlist_url
          : api.mediaUrl(session.playlist_url);

        reporter = new ProgressReporter(itemID);
        reporter.start(() => ({ positionMs: position, durationMs: duration }));

        if (avplay.available()) {
          // Tizen hardware path. AVPlay handles HLS demux + hardware
          // decode; the <video> element below stays unused.
          usingAvPlay = true;
          avplay.open(
            {
              url: fullURL,
              streamingMode: 'HLS',
              bearer: api.getToken() ?? undefined,
              startMs
            },
            {
              onProgress: (currentMs, durationMs) => {
                position = currentMs;
                duration = durationMs;
                if (loading) {
                  loading = false;
                  showControls();
                }
                // Marker + Up Next watchers ride on the same tick
                // AVPlay already gives us — no need for a second
                // timer.
                updateActiveMarker();
                maybeShowUpNext();
              },
              onEnded: () => {
                reporter?.stopped(duration, duration);
                // EOS auto-advance: episodes / podcasts go through
                // the Up Next overlay flow (which calls goToNext
                // when the user accepts or the countdown elapses);
                // music + audiobook chapters chain silently here
                // since the overlay's lead-in would clip the outro.
                if (nextSibling) {
                  goToNext(nextSibling);
                } else {
                  goto(`/item/${itemID}`);
                }
              },
              onError: (msg) => {
                error = msg;
                loading = false;
              }
            }
          );
          paused = false;
        } else {
          // Dev fallback: plain <video src=>. Won't demux HLS in
          // browser dev (we don't ship hls.js for Tizen), so treat
          // dev as a layout-only preview. Real playback testing
          // requires the TV.
          if (!video) return;
          video.src = fullURL;
          video.addEventListener('loadedmetadata', () => {
            if (startMs > 0 && video) video.currentTime = startMs / 1000;
            loading = false;
            void video?.play();
            showControls();
          });
          video.addEventListener('timeupdate', () => {
            position = Math.round((video?.currentTime ?? 0) * 1000);
            duration = Math.round((video?.duration ?? 0) * 1000);
            updateActiveMarker();
            maybeShowUpNext();
          });
          video.addEventListener('pause', () => {
            paused = true;
            reporter?.paused(position, duration);
            showControls();
          });
          video.addEventListener('play', () => {
            paused = false;
          });
          video.addEventListener('ended', () => {
            reporter?.stopped(duration, duration);
            if (nextSibling) {
              goToNext(nextSibling);
            } else {
              goto(`/item/${itemID}`);
            }
          });
        }
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
      if (usingAvPlay) avplay.close();
      if (controlsTimer) clearTimeout(controlsTimer);
      if (upNextTimer) clearInterval(upNextTimer);
      stopSyncStream();
    };
  });

  onDestroy(() => {
    if (usingAvPlay) avplay.close();
    stopSyncStream();
    if (upNextTimer) clearInterval(upNextTimer);
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

  <!-- Skip Intro / Skip Credits overlay. Visible while playhead
       is inside an active marker window; Enter skips, Back
       dismisses (key handling lives in onKey above). -->
  {#if activeMarker && !loading}
    <div class="skip-marker">
      Press OK to skip {activeMarker.kind === 'credits' ? 'Credits' : 'Intro'}
    </div>
  {/if}

  <!-- Up Next overlay — appears 25 s before EOS for episodes /
       podcasts. Music + audiobook chapters skip this and chain
       silently at EOS so the closing seconds play through. -->
  {#if upNextShown && nextSibling && !loading}
    <div class="up-next">
      <div class="up-next-label">UP NEXT · {upNextCountdown}s</div>
      <div class="up-next-title">{nextSibling.title}</div>
      <div class="up-next-hint">OK to play now · Back to dismiss</div>
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

  .skip-marker {
    position: absolute;
    bottom: 80px;
    right: 60px;
    padding: 14px 26px;
    background: var(--accent);
    color: #fff;
    font-size: var(--font-md);
    font-weight: 600;
    border-radius: 24px;
  }

  .up-next {
    position: absolute;
    top: 60px;
    right: 60px;
    padding: 18px 28px;
    background: rgba(7, 7, 13, 0.85);
    border: 2px solid var(--accent);
    border-radius: 12px;
    max-width: 360px;
  }
  .up-next-label {
    font-size: var(--font-sm);
    color: var(--accent);
    text-transform: uppercase;
    letter-spacing: 0.15em;
    margin-bottom: 6px;
  }
  .up-next-title {
    font-size: var(--font-md);
    color: var(--text-primary);
    margin-bottom: 10px;
  }
  .up-next-hint {
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }
</style>
