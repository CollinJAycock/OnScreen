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
  import { loadHls } from '$lib/player/hls-loader';
  import { ProgressReporter } from '$lib/player/progress-reporter';
  import { parseVtt, findCue, type TrickplayCue } from '$lib/player/trickplay';
  import type { OnlineSubtitle } from '$lib/api';

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
  let session: {
    session_id: string;
    token: string;
    playlist_url: string;
  } | null = null;
  let reporter: ProgressReporter | null = null;

  // Chapters: surface as jump targets. Start offsets used for green-button cycling.
  const chapters = $derived<Chapter[]>(item?.files[0]?.chapters ?? []);

  // Audio + subtitle stream metadata for the in-player pickers.
  // Yellow opens the audio picker, blue opens the subtitle picker —
  // standard TV-remote convention. Picking an audio track on an HLS
  // session re-issues the transcode with a new audio_stream_index;
  // subtitles toggle the corresponding video.textTracks entry.
  const audioStreams = $derived(item?.files[0]?.audio_streams ?? []);
  const subtitleStreams = $derived(item?.files[0]?.subtitle_streams ?? []);
  let audioPickerOpen = $state(false);
  let subtitlePickerOpen = $state(false);
  let pickerCursor = $state(0);
  // Index of the currently-active audio_stream within audio_streams.
  // Initialised to 0 since the first transcode session uses the
  // server's default audio (usually the first stream); switchAudioStream
  // updates it after a successful re-issue. Direct-play (not used on
  // webOS today — every webOS session is transcoded) would need a
  // separate textTracks-style API.
  let activeAudioIndex = $state(0);
  // Active subtitle: -1 means "off". Persisted via video.textTracks
  // mode rather than re-issued through the transcode session, since
  // the server emits subtitle streams as WebVTT lanes inside the HLS
  // playlist.
  let activeSubtitleIndex = $state(-1);

  // Intro / credits markers fetched alongside the item — drives the
  // Skip button overlay. Empty array for non-episode types and for
  // shows without auto-detected markers; either way the overlay
  // never renders.
  let markers = $state<Marker[]>([]);
  let activeMarker = $state<Marker | null>(null);
  // Per-marker dismiss set so the overlay doesn't re-pop when the
  // user scrubs back across a marker they already skipped or
  // explicitly dismissed. Keyed by start_ms (stable across reloads).
  const dismissedMarkers = new Set<number>();

  // Up Next: the chronologically-next episode of the current show /
  // season. Fetched on first item-load and surfaced as an overlay
  // 25 s before EOS for episodes / podcasts; for music tracks we
  // chain silently at EOS (no overlay) to avoid clipping the outro.
  let nextSibling = $state<ChildItem | null>(null);
  let upNextShown = $state(false);
  let upNextCountdown = $state(10);
  let upNextTimer: ReturnType<typeof setInterval> | null = null;

  // SSE subscription to /api/v1/notifications/stream for cross-
  // device resume sync. When the same user reports new progress on
  // another device while this player is paused, snap to that
  // position so resuming picks up where the other device left off.
  let syncEventSource: EventSource | null = null;
  // Echo guard: every saveProgress on this device round-trips back
  // as a sync event. Track the last position we reported so we can
  // ignore matches within a small window.
  let lastReportedPositionMs = -1;

  // Trickplay scrub-preview state. Cues parsed from the WebVTT
  // index on mount; null when the item has no sprite sheets
  // (movies that haven't been processed yet, audio-only items).
  // The active cue is recomputed reactively from `position`.
  let trickplayCues = $state<TrickplayCue[]>([]);
  const trickplayCue = $derived(
    trickplayCues.length > 0 ? findCue(trickplayCues, position) : null,
  );

  // Online subtitle search overlay. Opened from the subtitle
  // picker via "Find more online…" — searches OpenSubtitles via
  // the server, lets the user download a pick, and reloads the
  // item so the new external_subtitle row surfaces in
  // subtitle_streams.
  let onlineSubsOpen = $state(false);
  let onlineSubsLoading = $state(false);
  let onlineSubsResults = $state<OnlineSubtitle[]>([]);
  let onlineSubsCursor = $state(0);
  let onlineSubsError = $state('');
  let onlineSubsDownloading = $state(false);

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
    // Audio + subtitle pickers grab keys before everything else when
    // open — up/down moves cursor, enter selects, back closes.
    if (pickerKey(k)) return true;
    // Up Next overlay grabs the OK / Back keys when visible — Enter
    // accepts and chains, Back dismisses for the rest of this play.
    if (upNextShown) {
      if (k === 'enter' && nextSibling) { goToNext(nextSibling); return true; }
      if (k === 'back') { dismissUpNext(); return true; }
    }
    // Skip Intro / Skip Credits overlay handles Enter when visible
    // so the user doesn't have to find a button. Back dismisses it.
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
      case 'yellow':
        if (audioStreams.length > 1) openAudioPicker();
        return true;
      case 'blue':
        if (subtitleStreams.length > 0) openSubtitlePicker();
        return true;
    }
    return false;
  }

  // ── Audio + subtitle pickers ───────────────────────────────────────

  function openAudioPicker() {
    subtitlePickerOpen = false;
    pickerCursor = activeAudioIndex < 0 ? 0 : activeAudioIndex;
    audioPickerOpen = true;
    showControls();
  }

  function openSubtitlePicker() {
    audioPickerOpen = false;
    // Cursor offset: index 0 = "Off", indices 1..N = subtitle streams.
    pickerCursor = activeSubtitleIndex < 0 ? 0 : activeSubtitleIndex + 1;
    subtitlePickerOpen = true;
    showControls();
  }

  function closePickers() {
    audioPickerOpen = false;
    subtitlePickerOpen = false;
  }

  function pickerKey(k: RemoteKey): boolean {
    // Online-subtitle overlay takes priority — runs its own cursor
    // separate from the local-track picker because it has its own
    // open/close lifecycle (search → results → download).
    if (onlineSubsOpen) return onlineSubsKey(k);
    if (!audioPickerOpen && !subtitlePickerOpen) return false;
    if (k === 'back') { closePickers(); return true; }
    // Subtitle picker rows: Off (0), each stream (1..N), then a
    // synthetic "Find more online…" row. The picker doesn't gate on
    // the OpenSubtitles probe — `search` returns an empty list when
    // the server isn't configured for it, falling through harmlessly.
    const subtitleLen = subtitleStreams.length + 1 + 1; // +Off, +FindMore
    const len = audioPickerOpen ? audioStreams.length : subtitleLen;
    if (k === 'up') {
      pickerCursor = (pickerCursor - 1 + len) % len;
      return true;
    }
    if (k === 'down') {
      pickerCursor = (pickerCursor + 1) % len;
      return true;
    }
    if (k === 'enter') {
      if (audioPickerOpen) {
        const stream = audioStreams[pickerCursor];
        if (stream && pickerCursor !== activeAudioIndex) {
          void switchAudioStream(stream.index, pickerCursor);
        }
        closePickers();
      } else {
        const findMoreIdx = subtitleStreams.length + 1;
        if (pickerCursor === findMoreIdx) {
          closePickers();
          openOnlineSubtitleSearch();
          return true;
        }
        // pickerCursor === 0 is the synthetic "Off" row.
        applySubtitleSelection(pickerCursor === 0 ? -1 : pickerCursor - 1);
        closePickers();
      }
      return true;
    }
    return false;
  }

  // ── Online subtitle search ─────────────────────────────────────────

  async function openOnlineSubtitleSearch() {
    onlineSubsOpen = true;
    onlineSubsCursor = 0;
    onlineSubsError = '';
    onlineSubsLoading = true;
    onlineSubsResults = [];
    showControls();
    try {
      // No lang filter — the server enriches the query with the
      // item's title / year / IMDB id internally.
      onlineSubsResults = await endpoints.onlineSubtitles.search(itemID);
    } catch (e) {
      onlineSubsError = (e as Error).message ?? 'Search failed';
    } finally {
      onlineSubsLoading = false;
    }
  }

  function closeOnlineSubtitleSearch() {
    onlineSubsOpen = false;
    onlineSubsResults = [];
    onlineSubsError = '';
    onlineSubsCursor = 0;
  }

  function onlineSubsKey(k: RemoteKey): boolean {
    if (k === 'back') { closeOnlineSubtitleSearch(); return true; }
    if (onlineSubsLoading || onlineSubsDownloading) return true;
    const len = onlineSubsResults.length;
    if (len === 0) return true;
    if (k === 'up') { onlineSubsCursor = (onlineSubsCursor - 1 + len) % len; return true; }
    if (k === 'down') { onlineSubsCursor = (onlineSubsCursor + 1) % len; return true; }
    if (k === 'enter') {
      const pick = onlineSubsResults[onlineSubsCursor];
      if (pick) void downloadOnlineSubtitle(pick);
      return true;
    }
    return false;
  }

  async function downloadOnlineSubtitle(pick: OnlineSubtitle) {
    if (!item) return;
    const file = item.files[0];
    if (!file) return;
    onlineSubsDownloading = true;
    onlineSubsError = '';
    try {
      await endpoints.onlineSubtitles.download(itemID, file.id, pick);
      // Re-fetch the item so the fresh external_subtitle row shows
      // up in subtitle_streams. We don't auto-select since track-
      // ordering can shuffle once the new entry lands.
      const refreshed = await endpoints.items.get(itemID);
      item = refreshed;
      closeOnlineSubtitleSearch();
    } catch (e) {
      onlineSubsError = (e as Error).message ?? 'Download failed';
    } finally {
      onlineSubsDownloading = false;
    }
  }

  async function loadTrickplay() {
    // Item might not have trickplay generated yet (background
    // worker hasn't processed it, or it's audio-only). Either way,
    // leaving cues empty just suppresses the preview.
    try {
      const vtt = await endpoints.items.trickplayVtt(itemID);
      if (vtt) trickplayCues = parseVtt(vtt);
    } catch {
      /* leave empty */
    }
  }

  // Re-issue the active transcode session with a new
  // audio_stream_index, preserving the current playback position.
  // Server emits one audio per transcode session, so language
  // switching can't go through hls.js's track selector — only a
  // fresh session carries the chosen language.
  async function switchAudioStream(audioStreamIndex: number, pickerIndex: number) {
    if (!video || !item) return;
    const file = item.files[0];
    if (!file) return;
    const positionMs = position;
    try {
      const fresh = await endpoints.transcode.start({
        itemId: itemID,
        height: 1080,
        positionMs,
        fileId: file.id,
        supportsHEVC: true,
        audioStreamIndex,
      });
      // Tear down the previous hls.js instance before swapping the
      // source — fragments would otherwise keep buffering in the
      // background until destroyed.
      hls?.destroy();
      hls = null;
      session = fresh;

      const Hls = await loadHls();
      const fullURL = fresh.playlist_url.startsWith('http')
        ? fresh.playlist_url
        : api.mediaUrl(fresh.playlist_url);
      if (Hls.isSupported()) {
        const inst = new Hls({ lowLatencyMode: false });
        inst.loadSource(fullURL);
        inst.attachMedia(video);
        hls = inst;
      } else {
        video.src = fullURL;
      }
      video.addEventListener('loadedmetadata', () => {
        if (video) video.currentTime = positionMs / 1000;
        void video?.play();
      }, { once: true });
      activeAudioIndex = pickerIndex;
    } catch (e) {
      // Re-issue failed — keep the existing session running. The
      // user can try again from the picker.
      console.warn('audio re-issue failed', e);
    }
  }

  // Toggle a subtitle track via the video element's textTracks API.
  // Server-emitted WebVTT lanes show up as TextTrack entries when
  // the HLS playlist references them; the picker maps the user's
  // selection to a track mode of 'showing' / 'disabled'.
  function applySubtitleSelection(streamIndex: number) {
    if (!video) return;
    activeSubtitleIndex = streamIndex;
    const tracks = video.textTracks;
    for (let i = 0; i < tracks.length; i++) {
      tracks[i].mode = i === streamIndex ? 'showing' : 'disabled';
    }
  }

  // ── Markers ────────────────────────────────────────────────────────

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
    if (!m || !video) return;
    dismissedMarkers.add(m.start_ms);
    video.currentTime = Math.max(0, m.end_ms / 1000);
    activeMarker = null;
    showControls();
  }

  function dismissMarker() {
    if (!activeMarker) return;
    dismissedMarkers.add(activeMarker.start_ms);
    activeMarker = null;
  }

  // ── Up Next ────────────────────────────────────────────────────────

  // Lead window before EOS at which the overlay pops, and the
  // countdown that runs once it's visible. Match the Android
  // PlaybackFragment defaults so behaviour is consistent across
  // clients.
  const UP_NEXT_LEAD_MS = 25_000;
  const UP_NEXT_COUNTDOWN_SEC = 10;

  // Look up the "next" item to chain to on EOS — next episode of
  // the same season, next track on the same album, next chapter of
  // the same audiobook. parent_id + index is the universal pattern;
  // skipped silently for items that don't have one (movies,
  // standalone tracks, single-file audiobooks).
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
    // Music tracks + audiobook chapters chain silently at EOS —
    // skip the overlay entirely so the outro/closing-line plays
    // through.
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
    hls?.destroy();
    goto(`/watch/${target.id}`);
  }

  // ── Cross-device sync ──────────────────────────────────────────────

  function startSyncStream() {
    const origin = api.getOrigin();
    const tok = api.getToken();
    if (!origin || !tok) return;
    // EventSource doesn't support Authorization headers natively —
    // pass the bearer as a ?token= query param instead. Server
    // /notifications/stream is mounted under
    // RequiredAllowQueryToken so the access token still works
    // there even though the route isn't an asset endpoint.
    try {
      syncEventSource = new EventSource(
        `${origin}/api/v1/notifications/stream?token=${encodeURIComponent(tok)}`
      );
      syncEventSource.onmessage = onSyncEvent;
    } catch {
      // EventSource construction itself rarely throws; treat any
      // failure as "no sync today" and keep playing.
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
    if (!video || !data.data?.position_ms) return;
    // Only honour the sync when the local player is paused — if
    // the user is actively watching here, *this* device is the
    // authoritative position.
    if (!video.paused) return;
    // Self-loop guard: skip echoes within 2 s of our own most
    // recent saveProgress.
    const newPos = data.data.position_ms;
    if (lastReportedPositionMs >= 0 && Math.abs(newPos - lastReportedPositionMs) < 2000) {
      return;
    }
    video.currentTime = newPos / 1000;
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

        // Markers + next-sibling load in parallel with the playback
        // session — neither is on the critical path; failures are
        // best-effort (an empty marker list just means no Skip
        // button, no Up Next means natural EOS exits).
        void loadMarkers();
        void loadNextSibling();
        void loadTrickplay();
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
          error = 'HLS playback is not supported on this device.';
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
          // Marker watcher: surface a "Skip Intro" / "Skip Credits"
          // overlay while the playhead is inside an active window,
          // unless the user has already dismissed that marker.
          updateActiveMarker();
          // Up Next watcher: 25 s before EOS, pop the overlay so
          // the user can hit Enter to skip the credits early or
          // let the countdown auto-advance.
          maybeShowUpNext();
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
          // EOS auto-advance: episodes / podcasts use the Up Next
          // overlay (which calls goToNext when the user accepts or
          // the countdown elapses). Music tracks chain silently —
          // no overlay because the album-side outro sits in the
          // last few seconds and should play in full. Movies and
          // standalone items pop back to the detail page.
          if (nextSibling && (item?.type === 'track' || item?.type === 'audiobook_chapter')) {
            goToNext(nextSibling);
          } else if (nextSibling) {
            goToNext(nextSibling);
          } else {
            goto(`/item/${itemID}`);
          }
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
      if (upNextTimer) clearInterval(upNextTimer);
      stopSyncStream();
    };
  });

  onDestroy(() => {
    hls?.destroy();
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

  <!-- Skip Intro / Skip Credits overlay. Shown while playhead is
       inside an active marker window; OK skips, Back dismisses
       (key handling lives in onKey above). -->
  {#if activeMarker && !loading}
    <div class="skip-marker">
      Press OK to skip {activeMarker.kind === 'credits' ? 'Credits' : 'Intro'}
    </div>
  {/if}

  <!-- Audio + subtitle pickers. Yellow opens audio (HLS re-issues the
       session with the chosen audio_stream_index, preserving position);
       blue opens subtitles (toggles a video.textTracks entry). The
       overlay floats above the video; arrow keys move the cursor,
       OK selects, Back closes. -->
  {#if audioPickerOpen}
    <div class="picker">
      <div class="picker-title">Audio</div>
      {#each audioStreams as s, i (s.index)}
        <div class="picker-row" class:active={i === pickerCursor} class:current={i === activeAudioIndex}>
          {#if i === activeAudioIndex}● {/if}
          {s.language || 'und'} · {s.codec} · {s.channels}ch{#if s.title}{` · ${s.title}`}{/if}
        </div>
      {/each}
    </div>
  {/if}

  {#if subtitlePickerOpen}
    <div class="picker">
      <div class="picker-title">Subtitles</div>
      <div class="picker-row" class:active={pickerCursor === 0} class:current={activeSubtitleIndex === -1}>
        {#if activeSubtitleIndex === -1}● {/if}
        Off
      </div>
      {#each subtitleStreams as s, i (s.index)}
        <div class="picker-row"
             class:active={pickerCursor === i + 1}
             class:current={i === activeSubtitleIndex}>
          {#if i === activeSubtitleIndex}● {/if}
          {s.language || 'und'}{#if s.forced}{' · forced'}{/if}{#if s.title}{` · ${s.title}`}{/if}
        </div>
      {/each}
      <div class="picker-row picker-row-action"
           class:active={pickerCursor === subtitleStreams.length + 1}>
        Find more online…
      </div>
    </div>
  {/if}

  {#if onlineSubsOpen}
    <div class="picker online-subs">
      <div class="picker-title">Online subtitles</div>
      {#if onlineSubsLoading}
        <div class="picker-row">Searching…</div>
      {:else if onlineSubsError}
        <div class="picker-row picker-row-error">{onlineSubsError}</div>
      {:else if onlineSubsResults.length === 0}
        <div class="picker-row">No results — Back to close.</div>
      {:else}
        {#each onlineSubsResults as r, i (r.provider_file_id)}
          <div class="picker-row" class:active={onlineSubsCursor === i}>
            <div class="online-sub-line">
              <span class="online-sub-lang">{r.language || 'und'}</span>
              <span class="online-sub-name">{r.file_name}</span>
            </div>
            <div class="online-sub-meta">
              {#if r.from_trusted}<span>trusted</span>{/if}
              {#if r.hd}<span>hd</span>{/if}
              {#if r.hearing_impaired}<span>SDH</span>{/if}
              {#if r.download_count}<span>{r.download_count.toLocaleString()} dl</span>{/if}
              {#if r.uploader_name}<span>by {r.uploader_name}</span>{/if}
            </div>
          </div>
        {/each}
      {/if}
      {#if onlineSubsDownloading}
        <div class="picker-row">Downloading…</div>
      {/if}
    </div>
  {/if}

  <!-- Up Next overlay — appears 25 s before EOS for episodes /
       podcasts. Music tracks + audiobook chapters skip this and
       chain silently at EOS so the outro plays through. -->
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
            {#if trickplayCue && duration > 0}
              <!-- Sprite-cropped scrub preview. The element sized to
                   (w, h) reveals only the cue's region of the parent
                   sprite via background-position. Anchored to the
                   track so the percent-based `left` lands on the
                   playhead regardless of surrounding layout. -->
              <div
                class="trickplay-preview"
                style="
                  left: {progressPct}%;
                  width: {trickplayCue.w}px;
                  height: {trickplayCue.h}px;
                  margin-left: -{trickplayCue.w / 2}px;
                  background-image: url({endpoints.items.trickplaySpriteUrl(itemID, trickplayCue.spritePath)});
                  background-position: -{trickplayCue.x}px -{trickplayCue.y}px;
                "
              ></div>
            {/if}
          </div>
          <div class="remaining">{fmt(duration - position)}</div>
        </div>

        <div class="hints">
          <span>← → seek 10s</span>
          <span>◀◀ ▶▶ seek 30s</span>
          <span>OK play/pause</span>
          {#if chapters.length > 0}<span>red/green chapters</span>{/if}
          {#if audioStreams.length > 1}<span>yellow audio</span>{/if}
          {#if subtitleStreams.length > 0}<span>blue subtitles</span>{/if}
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

  .picker {
    position: absolute;
    top: 80px;
    right: 60px;
    padding: 24px 32px;
    background: rgba(7, 7, 13, 0.92);
    border: 2px solid var(--border-strong);
    border-radius: 12px;
    min-width: 360px;
    max-width: 520px;
  }
  .picker-title {
    font-size: var(--font-sm);
    text-transform: uppercase;
    letter-spacing: 0.15em;
    color: var(--text-secondary);
    margin-bottom: 12px;
  }
  .picker-row {
    font-size: var(--font-md);
    color: var(--text-primary);
    padding: 8px 12px;
    border-radius: 6px;
    white-space: pre-wrap;
  }
  .picker-row.active {
    background: var(--accent);
    color: white;
  }
  .picker-row.current:not(.active) {
    color: var(--accent);
  }
  .picker-row-action {
    margin-top: 6px;
    border-top: 1px solid rgba(255, 255, 255, 0.08);
    padding-top: 12px;
    font-style: italic;
    color: var(--text-secondary);
  }
  .picker-row-action.active {
    color: white;
    font-style: normal;
  }
  .picker-row-error { color: #fca5a5; }
  .online-subs {
    /* Wider than the local picker — file names + uploader chips
       are longer than language tags. */
    min-width: 560px;
    max-width: 760px;
  }
  .online-sub-line {
    display: flex;
    gap: 12px;
    align-items: baseline;
  }
  .online-sub-lang {
    font-weight: 600;
    text-transform: uppercase;
    color: var(--accent);
  }
  .online-sub-name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .picker-row.active .online-sub-lang { color: white; }
  .online-sub-meta {
    display: flex;
    gap: 12px;
    font-size: var(--font-sm);
    color: var(--text-secondary);
    margin-top: 4px;
  }
  .picker-row.active .online-sub-meta {
    color: rgba(255, 255, 255, 0.85);
  }

  /* Sprite-cropped trickplay preview. Anchored to .track (which is
     position: relative). `left: <pct>` places the preview's left
     edge at the playhead, then `margin-left: -w/2` centres it.
     Sprite cropping is pure CSS: background-image is the full
     sprite sheet, background-position shifts to the cue's xywh
     origin, the element's size masks the rest. */
  .trickplay-preview {
    position: absolute;
    bottom: 32px;
    border: 2px solid rgba(255, 255, 255, 0.6);
    border-radius: 4px;
    background-repeat: no-repeat;
    background-size: auto;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.6);
    pointer-events: none;
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
