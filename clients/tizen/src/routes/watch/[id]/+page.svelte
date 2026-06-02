<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import {
    api,
    endpoints,
    ApiError,
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
  import { parseVtt, findCue, type TrickplayCue } from '$lib/player/trickplay';
  import type { OnlineSubtitle } from '$lib/api';

  const itemID = page.params.id!;
  // Fallback HTML5 video element — used only when AVPlay isn't
  // available (i.e., `vite dev` against a desktop browser). On real
  // Tizen hardware AVPlay renders to a hardware overlay behind the
  // webview; the <video> element stays unused.
  let video: HTMLVideoElement | undefined = $state();
  // Tizen-specific anchor `<object type="application/avplayer">` —
  // the firmware uses this DOM element as the placement marker for
  // AVPlay's hardware video overlay. Must be a direct child of <body>
  // (Jellyfin + Samsung samples both do this); nested under Svelte's
  // .tv-root / .player wrappers, the firmware's chroma reservation
  // can be clipped by overflow:hidden or fail to engage at all.
  // Created imperatively on mount, removed on destroy.
  let avplayAnchor: HTMLObjectElement | null = null;

  let item = $state<ItemDetail | null>(null);
  let error = $state('');
  let loading = $state(true);
  let paused = $state(true);
  let position = $state(0);
  let duration = $state(0);
  let controlsVisible = $state(true);
  let controlsTimer: ReturnType<typeof setTimeout> | null = null;

  let session: {
    session_id: string;
    token: string;
    playlist_url: string;
  } | null = null;
  let reporter: ProgressReporter | null = null;
  let usingAvPlay = $state(false);

  // Lightweight breadcrumb logger — routes to the debug console only.
  // Used to ship as an on-screen HUD; the HUD is gone now that the
  // major bring-up issues are fixed, but the calls stay so re-enabling
  // (just wire dbg into a state array) is a one-line change.
  function dbg(msg: string) {
    console.debug('[watch]', msg);
  }

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
  let activeAudioIndex = $state(0);
  let activeSubtitleIndex = $state(-1);

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
  let prevSibling = $state<ChildItem | null>(null);
  // For music tracks: the album (parent) + artist (grandparent) and
  // the position in the album's track list. Surfaced in the now-
  // playing UI so the user sees "Track 3 of 12" + album/artist
  // without having to back out to the album page.
  let albumTitle = $state('');
  let artistTitle = $state('');
  let queuePosition = $state(0); // 1-indexed, 0 = unknown
  let queueTotal = $state(0);
  let upNextShown = $state(false);
  let upNextCountdown = $state(10);
  let upNextTimer: ReturnType<typeof setInterval> | null = null;

  // Cross-device resume sync via SSE. When the same user reports
  // new progress on another device while this player is paused,
  // snap to that position so resume picks up where the other
  // device left off. Active local playback wins.
  let syncEventSource: EventSource | null = null;
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
  // subtitle_streams. Tracked separately from the local subtitle
  // picker so the two can be open in sequence without state
  // overlap.
  let onlineSubsOpen = $state(false);
  let onlineSubsLoading = $state(false);
  let onlineSubsResults = $state<OnlineSubtitle[]>([]);
  let onlineSubsCursor = $state(0);
  let onlineSubsError = $state('');
  let onlineSubsDownloading = $state(false);

  // Item types that have no video — keep controls (scrubber, title,
  // play/pause) visible permanently since there's no picture to dim
  // behind them. Video items get the standard auto-hide-after-3s.
  const isAudioItem = $derived(
    !!item && (
      item.type === 'track' ||
      item.type === 'audiobook' ||
      item.type === 'audiobook_chapter'
    )
  );

  function showControls() {
    controlsVisible = true;
    if (controlsTimer) clearTimeout(controlsTimer);
    if (isAudioItem) return; // never auto-hide for music / audiobooks
    controlsTimer = setTimeout(() => (controlsVisible = false), 3000);
  }

  // Cancel any pending auto-hide once we know the item is audio.
  // showControls() can fire before item loads, queuing a timer; when
  // the item resolves to a track we want controls to stay up.
  $effect(() => {
    if (isAudioItem) {
      if (controlsTimer) {
        clearTimeout(controlsTimer);
        controlsTimer = null;
      }
      controlsVisible = true;
    }
  });

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
    goto(`#/item/${itemID}`);
  }

  function onKey(k: RemoteKey): boolean {
    // Audio + subtitle pickers grab keys before everything else when
    // open — up/down moves cursor, enter selects, back closes.
    if (pickerKey(k)) return true;
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
        // For music tracks the rewind button is a "previous track"
        // skip — common audio-player convention. Fall back to a
        // 30 s seek for video / audiobook chapters where chapter
        // ordering is rare and seeking inside the current track is
        // more useful.
        if (item?.type === 'track' && prevSibling) {
          goToNext(prevSibling);
        } else {
          seek(-30_000);
        }
        return true;
      case 'forward':
        // Mirror of rewind: music = next-track skip, otherwise seek.
        if (item?.type === 'track' && nextSibling) {
          goToNext(nextSibling);
        } else {
          seek(30_000);
        }
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
    pickerCursor = activeSubtitleIndex < 0 ? 0 : activeSubtitleIndex + 1;
    subtitlePickerOpen = true;
    showControls();
  }

  function closePickers() {
    audioPickerOpen = false;
    subtitlePickerOpen = false;
  }

  function pickerKey(k: RemoteKey): boolean {
    // Online subtitle overlay takes priority — runs its own cursor
    // separate from the local-track picker because it has its own
    // open/close lifecycle (search → results → download).
    if (onlineSubsOpen) return onlineSubsKey(k);
    if (!audioPickerOpen && !subtitlePickerOpen) return false;
    if (k === 'back') { closePickers(); return true; }
    // Subtitle picker rows: Off (0), each stream (1..N), then a
    // synthetic "Find more online…" row when the server has the
    // OpenSubtitles search wired (which the picker doesn't gate on
    // — the search call returns an empty list if the server isn't
    // configured, falling through harmlessly).
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
        // Find-more row is the last index. Other rows: 0 = Off,
        // 1..N = local streams (1-shifted by the synthetic Off row).
        const findMoreIdx = subtitleStreams.length + 1;
        if (pickerCursor === findMoreIdx) {
          closePickers();
          openOnlineSubtitleSearch();
          return true;
        }
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
      // item's title / year / IMDB id internally, so a "wide net"
      // search is the right default. Tightening to a specific
      // language is a follow-up that needs a per-user pref read
      // (the language picker UI doesn't exist on Tizen yet).
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
      // up in subtitle_streams — caller can then open the local
      // picker and select it. We don't auto-select because language
      // / track-ordering can shuffle once the new entry lands.
      const refreshed = await endpoints.items.get(itemID);
      item = refreshed;
      closeOnlineSubtitleSearch();
    } catch (e) {
      onlineSubsError = (e as Error).message ?? 'Download failed';
    } finally {
      onlineSubsDownloading = false;
    }
  }

  // Re-issue the active transcode session with a new
  // audio_stream_index, preserving the current playback position.
  // Server emits one audio per transcode session, so language
  // switching can't go through the player's track selector — only a
  // fresh session carries the chosen language.
  async function switchAudioStream(audioStreamIndex: number, pickerIndex: number) {
    if (!item) return;
    const file = item.files[0];
    if (!file) return;
    const positionMs = position;
    try {
      // Defer to the platform-specific implementation in onMount —
      // both AVPlay (HW path) and HTML5 hls.js are torn down + rebuilt
      // around the new playlist URL. The session pointer is captured
      // at module scope so subsequent saves go through the new id.
      await reissueTranscode(positionMs, audioStreamIndex);
      activeAudioIndex = pickerIndex;
    } catch (e) {
      console.warn('audio re-issue failed', e);
    }
  }

  // Re-issue the active transcode session at [positionMs] with the
  // chosen [audioStreamIndex]. Tears down the prior AVPlay / HTML5
  // session, kicks off a new transcode session, opens the new HLS
  // playlist on the same player. Server cleans up the old session
  // when the new one starts; we still call stop() best-effort to
  // free up resources sooner.
  async function reissueTranscode(positionMs: number, audioStreamIndex: number) {
    if (!item) return;
    const file = item.files[0];
    if (!file) return;
    if (session) {
      void endpoints.transcode.stop(session.session_id, session.token).catch(() => {});
    }
    if (usingAvPlay) avplay.close();
    const fresh = await endpoints.transcode.start({
      itemId: itemID,
      // 2160 = the Q80B-class 4K hardware decoder's ceiling. Asking
      // for 1080 forces the server to downscale + tonemap 4K HDR
      // sources, which can't sustain real-time on the CPU pipeline
      // and AVPlay times out on the slow first segment. With 2160 the
      // server's decision tree picks `-c:v copy` for HEVC sources at
      // ≤2160 and we direct-play. SDR-only / 1080p Tizen models exist
      // but are rare in the 2020+ install base; can be probed later.
      height: 2160,
      positionMs,
      fileId: file.id,
      supportsHEVC: true,
      audioStreamIndex,
    });
    session = fresh;
    const freshURL = fresh.playlist_url.startsWith('http')
      ? fresh.playlist_url
      : api.mediaUrl(fresh.playlist_url);
    if (usingAvPlay) {
      if (!avplayAnchor) return;
      avplay.open(
        {
          url: freshURL,
          streamingMode: 'HLS',
          bearer: api.getToken() ?? undefined,
          startMs: positionMs,
          anchor: avplayAnchor,
        },
        {
          onProgress: (currentMs, durationMs) => {
            position = currentMs;
            duration = durationMs;
            updateActiveMarker();
            maybeShowUpNext();
          },
          onEnded: () => {
            reporter?.stopped(duration, duration);
            if (nextSibling) goToNext(nextSibling);
            else goto(`#/item/${itemID}`);
          },
          onError: (msg) => { error = msg; },
        },
      );
      paused = false;
    } else if (video) {
      video.src = freshURL;
      video.addEventListener('loadedmetadata', () => {
        if (video) video.currentTime = positionMs / 1000;
        void video?.play();
      }, { once: true });
    }
  }

  // Toggle a subtitle track via the video element's textTracks API.
  // For the AVPlay path (HW HEVC/AV1), subtitles ride the player's
  // own track selector — this fallback only fires on the HTML5
  // <video> path.
  function applySubtitleSelection(streamIndex: number) {
    activeSubtitleIndex = streamIndex;
    if (usingAvPlay) {
      // AVPlay's track index API differs by firmware. Best-effort:
      // try webapis.avplay.setSelectTrack('TEXT', index) where
      // negative disables.
      try {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        (window as any).webapis?.avplay?.setSelectTrack?.('TEXT', streamIndex);
      } catch {
        /* firmware-quirk fallback — silent */
      }
      return;
    }
    if (!video) return;
    const tracks = video.textTracks;
    for (let i = 0; i < tracks.length; i++) {
      tracks[i].mode = i === streamIndex ? 'showing' : 'disabled';
    }
  }

  // ── Markers ──────────────────────────────────────────────────────

  async function loadMarkers() {
    try {
      markers = await endpoints.items.markers(itemID);
    } catch {
      markers = [];
    }
  }

  async function loadTrickplay() {
    // Item might not have trickplay generated yet (background
    // worker hasn't processed it, or it's audio-only). Either
    // way, leaving cues empty just suppresses the preview.
    try {
      const vtt = await endpoints.items.trickplayVtt(itemID);
      if (vtt) trickplayCues = parseVtt(vtt);
    } catch {
      /* leave empty */
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
      const sorted = kids
        .filter((k) => k.type === item!.type && k.index != null)
        .sort((a, b) => (a.index ?? 0) - (b.index ?? 0));
      const myIdx = item.index ?? -1;
      nextSibling = sorted.find((k) => (k.index ?? -1) === myIdx + 1) ?? null;
      prevSibling = sorted.find((k) => (k.index ?? -1) === myIdx - 1) ?? null;
      // Queue position — 1-indexed slot within the same-type siblings.
      const myI = sorted.findIndex((k) => k.id === item!.id);
      if (myI >= 0) {
        queuePosition = myI + 1;
        queueTotal = sorted.length;
      }
    } catch {
      // Best-effort.
    }
  }

  // For music tracks: fetch album (parent) + artist (grandparent) so
  // the now-playing view can label them. Cheap — two items.get calls
  // off the critical playback path, run in parallel.
  async function loadAudioContext() {
    if (!item || item.type !== 'track' || !item.parent_id) return;
    try {
      const album = await endpoints.items.get(item.parent_id);
      albumTitle = album.title ?? '';
      if (album.parent_id) {
        const artist = await endpoints.items.get(album.parent_id);
        artistTitle = artist.title ?? '';
      }
    } catch {
      // Best-effort — leave labels blank on failure.
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
    goto(`#/watch/${target.id}`);
  }

  // ── Cross-device sync ────────────────────────────────────────────

  function startSyncStream() {
    const origin = api.getOrigin();
    // SSE can't carry an Authorization header — pass the purpose=asset
    // token in ?token=. The server rejects a general access token in a
    // URL; the asset token authenticates /notifications/stream.
    const tok = api.getAssetToken();
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

  // Loading overlay element — created imperatively as a direct child
  // of <body> so it sits above AVPlay's hardware overlay in the
  // firmware compositor's stacking order. Anything Svelte renders
  // inside .player gets hidden by AVPlay between open() and the first
  // decoded frame; a body-level fixed div with z-index 9999999 is
  // the standard fix (Jellyfin uses the same pattern at
  // jellyfin-web/src/components/loading/loading.ts).
  let loadingEl: HTMLDivElement | null = null;
  function ensureLoadingEl() {
    if (loadingEl) return loadingEl;
    const el = document.createElement('div');
    el.style.cssText = [
      'position: fixed',
      'top: 0', 'left: 0', 'right: 0', 'bottom: 0',
      'z-index: 9999999',
      'display: none',
      'align-items: center',
      'justify-content: center',
      'flex-direction: column',
      'gap: 28px',
      'background: rgba(0, 0, 0, 0.75)',
      'color: #fff',
      'font-family: inherit',
    ].join(';');
    el.innerHTML = `
      <div style="
        width: 120px; height: 120px;
        border: 12px solid rgba(255, 255, 255, 0.2);
        border-top-color: #7c6af7;
        border-radius: 50%;
        animation: tv-loading-spin 0.9s linear infinite;
      "></div>
      <div data-label style="font-size: 36px; font-weight: 500;">Starting playback…</div>
      <div data-sub style="font-size: 28px; color: rgba(255, 255, 255, 0.7);"></div>
    `;
    // Inject keyframes once; can't live in scoped CSS since the element
    // isn't a Svelte node anymore.
    if (!document.getElementById('tv-loading-keyframes')) {
      const style = document.createElement('style');
      style.id = 'tv-loading-keyframes';
      style.textContent = '@keyframes tv-loading-spin { to { transform: rotate(360deg); } }';
      document.head.appendChild(style);
    }
    document.body.appendChild(el);
    loadingEl = el;
    return el;
  }
  function showLoading(subtitle = '') {
    const el = ensureLoadingEl();
    const sub = el.querySelector<HTMLElement>('[data-sub]');
    if (sub) sub.textContent = subtitle;
    el.style.display = 'flex';
  }
  function hideLoading() {
    if (loadingEl) loadingEl.style.display = 'none';
  }
  function destroyLoadingEl() {
    if (loadingEl && loadingEl.parentNode) {
      loadingEl.parentNode.removeChild(loadingEl);
    }
    loadingEl = null;
  }
  // Mirror the `loading` reactive state onto the imperative element.
  $effect(() => {
    if (loading) showLoading(item?.title ?? '');
    else hideLoading();
  });

  // Wire the <video> element's progress / pause / play / ended events
  // into the same reactive state the AVPlay path drives. Reused by
  // the audio-only direct-play branch (every TV) and the dev fallback
  // (browser `vite dev`).
  function attachVideoListeners(v: HTMLVideoElement, startMs: number) {
    v.addEventListener('loadedmetadata', () => {
      if (startMs > 0) v.currentTime = startMs / 1000;
      loading = false;
      void v.play();
      showControls();
    });
    v.addEventListener('timeupdate', () => {
      position = Math.round(v.currentTime * 1000);
      const serverDurationMs = item?.duration_ms ?? item?.files[0]?.duration_ms ?? 0;
      if (serverDurationMs > 0) {
        duration = serverDurationMs;
      } else {
        const vd = v.duration ?? 0;
        duration = Number.isFinite(vd) ? Math.round(vd * 1000) : 0;
      }
      updateActiveMarker();
      maybeShowUpNext();
    });
    v.addEventListener('pause', () => {
      paused = true;
      reporter?.paused(position, duration);
      showControls();
    });
    v.addEventListener('play', () => {
      paused = false;
    });
    v.addEventListener('ended', () => {
      reporter?.stopped(duration, duration);
      if (nextSibling) {
        goToNext(nextSibling);
      } else {
        goto(`#/item/${itemID}`);
      }
    });
  }

  // The body-level transparency tricks + the <object application/avplayer>
  // anchor are needed ONLY when AVPlay actually engages (video items).
  // For audio playback we use the HTML5 <video> path and want the page
  // to render normally; an empty AVPlay <object> sitting at the body
  // root paints opaque firmware-black and would cover the music view.
  function enableAvplayCompositing() {
    document.documentElement.classList.add('player-route');
    document.body.classList.add('player-route');
    if (avplayAnchor || !avplay.available()) return;
    const a = document.createElement('object') as HTMLObjectElement;
    a.type = 'application/avplayer';
    a.setAttribute('width', '1920');
    a.setAttribute('height', '1080');
    a.style.position = 'absolute';
    a.style.left = '0';
    a.style.top = '0';
    a.style.width = '1920px';
    a.style.height = '1080px';
    a.style.zIndex = '0';
    document.body.insertBefore(a, document.body.firstChild);
    avplayAnchor = a;
  }
  function disableAvplayCompositing() {
    document.documentElement.classList.remove('player-route');
    document.body.classList.remove('player-route');
    if (avplayAnchor && avplayAnchor.parentNode) {
      avplayAnchor.parentNode.removeChild(avplayAnchor);
    }
    avplayAnchor = null;
  }

  // Map a server PARENTAL_LIMIT reason to a friendly sentence for the
  // block overlay. Mirrors the web + webOS + phone clients.
  function parentalBlockMessage(reason: string): string {
    if (reason === 'outside_allowed_hours')
      return 'Outside the allowed hours for this account. Try again during the permitted times.';
    if (reason === 'daily_limit_reached')
      return "Today's watch-time limit for this account has been reached. Check back tomorrow.";
    return 'Playback is blocked by a parental watch limit on this account.';
  }

  // Stop playback and show the block message. Pauses whichever surface is
  // active (AVPlay or the <video> element).
  function applyParentalBlock(reason: string) {
    reporter?.stop();
    try {
      if (usingAvPlay) avplay.pause();
      else video?.pause();
    } catch { /* ignore */ }
    error = parentalBlockMessage(reason);
    loading = false;
  }

  onMount(() => {
    const offKey = focusManager.pushKeyHandler(onKey);

    (async () => {
      try {
        dbg(`fetch /items/${itemID}`);
        item = await endpoints.items.get(itemID);
        dbg(`item: type=${item.type} files=${item.files.length} title=${item.title.slice(0, 40)}`);
        if (item.type === 'book') {
          // Ebooks need a paginated reader, not the video pipeline.
          // Defence-in-depth — the item detail page already hides the
          // Play button for book type, but a stray deep link or older
          // build elsewhere could still land us here.
          error = 'Book reading isn’t available on TV. Open this book in the web or phone app.';
          loading = false;
          return;
        }
        if (item.files.length === 0) {
          error = 'No playable file for this item.';
          loading = false;
          return;
        }

        // Parental watch-limit pre-flight — block a restricted user before
        // any stream/transcode starts. Fail-open if the check itself errors;
        // the progress heartbeat below still catches a cap reached mid-session.
        try {
          const wl = await endpoints.users.watchLimit();
          if (!wl.allowed) {
            error = parentalBlockMessage(wl.reason ?? '');
            loading = false;
            return;
          }
        } catch { /* limit lookup failed — fail open, allow playback */ }

        // Markers + next-sibling load alongside the playback
        // session — neither blocks the start. Failures are
        // best-effort (empty marker list = no Skip button; no
        // next sibling = natural EOS exit).
        void loadMarkers();
        void loadNextSibling();
        void loadAudioContext();
        void loadTrickplay();
        startSyncStream();

        const file = item.files[0];
        const startMs = item.view_offset_ms ?? 0;
        dbg(`file: vc=${file.video_codec || '∅'} ac=${file.audio_codec || '∅'} ${file.resolution_w || '?'}x${file.resolution_h || '?'}`);

        // Audio-only items skip the HLS transcode path. The TV's
        // <video> element plays MP3/AAC/M4A/M4B/FLAC directly, which
        // also avoids AVPlay's video-only overlay path and the
        // transcode session's loadedmetadata stall that triggers the
        // "Starting playback…" hang for audio-only HLS playlists.
        //
        // Prefer item.type to detect — "audiobook (Illustrated)"
        // editions sometimes include a real per-chapter slideshow
        // video stream, so the codec heuristic alone misclassifies
        // them. For podcasts the type can host video too, so we keep
        // the codec check there.
        const audioType =
          item.type === 'track' ||
          item.type === 'audiobook' ||
          item.type === 'audiobook_chapter';
        const isAudioOnly = audioType || (!file.video_codec && !!file.audio_codec);

        if (isAudioOnly) {
          if (!video) {
            error = 'Player element missing.';
            loading = false;
            return;
          }
          reporter = new ProgressReporter(itemID);
          reporter.start(() => ({ positionMs: position, durationMs: duration }));
          // Attach listeners BEFORE setting src — otherwise a fast
          // loadedmetadata fires before the handler is wired, loading
          // stays true forever, and the music view never appears.
          attachVideoListeners(video, startMs);
          video.src = api.assetUrl(file.stream_url);
          // Belt + suspenders: some webviews don't fire loadedmetadata
          // for audio-only sources reliably. If we haven't flipped
          // loading inside 8 s, force it off so the music view paints.
          setTimeout(() => {
            if (loading) {
              dbg('audio loadedmetadata timeout — forcing loading=false');
              loading = false;
              void video?.play();
              showControls();
            }
          }, 8000);
          return;
        }

        // Request video-copy (server-side `-c:v copy`) when the
        // source codec is AVPlay-decodable. HEVC and H.264 are both
        // hardware-decoded on Q80B-class Tizen panels; HDR10/HLG is
        // also native. Without video_copy, the server transcodes 4K
        // HDR through a CPU tonemap chain that takes ~50–70 s per
        // first segment and AVPlay times out (the NeverEnding Story
        // 4K HEVC HDR symptom). Mirrors the web client's
        // canRemuxVideo logic.
        const sourceCodec = (file.video_codec ?? '').toLowerCase();
        const localCodecOK = sourceCodec === 'hevc' || sourceCodec === 'h265' ||
                             sourceCodec === 'h264' || sourceCodec === 'avc' ||
                             sourceCodec === 'av1';
        // Server-authoritative play decision (capability profiles): the server
        // uses our X-Client-Capabilities header. directPlay/directStream → copy
        // the video; transcode → re-encode. Falls back to the local codec check
        // when the call fails. See docs/capability-profiles.md.
        let serverVerdict: string | null = null;
        try {
          serverVerdict = (await endpoints.transcode.decide(itemID, file.id)).decision;
        } catch (e) {
          dbg('playback-decision failed, using local codec check: ' + e);
        }
        const codecOK = serverVerdict ? serverVerdict !== 'transcode' : localCodecOK;
        const videoCopy = codecOK && file.faststart !== false;
        dbg(`decision: server=${serverVerdict} codecOK=${codecOK} → transcode.start (video_copy=${videoCopy}) …`);
        session = await endpoints.transcode.start({
          itemId: itemID,
          // 2160 = the Q80B-class 4K hardware decoder's ceiling.
          height: 2160,
          positionMs: startMs,
          fileId: file.id,
          supportsHEVC: true,
          videoCopy,
        });
        dbg(`transcode session: ${session.session_id.slice(0, 8)}…`);

        const fullURL = session.playlist_url.startsWith('http')
          ? session.playlist_url
          : api.mediaUrl(session.playlist_url);
        dbg(`url: ${fullURL.slice(0, 60)}…`);

        reporter = new ProgressReporter(itemID);
        reporter.start(
          () => ({ positionMs: position, durationMs: duration }),
          (reason) => applyParentalBlock(reason)
        );

        if (avplay.available()) {
          // Tizen hardware path. AVPlay handles HLS demux +
          // hardware decode; the <video> element below stays unused.
          usingAvPlay = true;
          enableAvplayCompositing();
          dbg('avplay.open …');
          // Surface silent AVPlay failures as a visible error after
          // 30 s with no progress tick. Without this the user sees a
          // permanent black screen if prepareAsync hangs on a stream
          // the firmware can't decode (rare 4K HEVC profiles, exotic
          // audio channel layouts).
          let watchdog: ReturnType<typeof setTimeout> | null = setTimeout(() => {
            if (loading) {
              error = 'Playback didn’t start. The TV may not support this stream.';
              loading = false;
              try { avplay.close(); } catch { /* ignore */ }
            }
          }, 30_000);
          const clearWatchdog = () => {
            if (watchdog) {
              clearTimeout(watchdog);
              watchdog = null;
            }
          };
          if (!avplayAnchor) {
            clearWatchdog();
            error = 'AVPlay anchor element not mounted.';
            loading = false;
            return;
          }
          dbg(`anchor rect: ${avplayAnchor.offsetLeft},${avplayAnchor.offsetTop},${avplayAnchor.offsetWidth},${avplayAnchor.offsetHeight}`);
          avplay.open(
            {
              url: fullURL,
              streamingMode: 'HLS',
              bearer: api.getToken() ?? undefined,
              startMs,
              anchor: avplayAnchor
            },
            {
              onProgress: (currentMs, durationMs) => {
                clearWatchdog();
                position = currentMs;
                duration = durationMs;
                // Only flip loading once AVPlay reports forward
                // progress (currentMs > 0). oncurrentplaytime can
                // fire with ms=0 while still preparing/buffering,
                // and clearing the loading overlay too early hides
                // the spinner before the picture lands — user sees
                // no spinner, just a brief blank then video.
                if (loading && currentMs > 0) {
                  dbg(`onProgress (first real tick): ${currentMs}/${durationMs} ms`);
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
                clearWatchdog();
                reporter?.stopped(duration, duration);
                // EOS auto-advance: episodes / podcasts go through
                // the Up Next overlay flow (which calls goToNext
                // when the user accepts or the countdown elapses);
                // music + audiobook chapters chain silently here
                // since the overlay's lead-in would clip the outro.
                if (nextSibling) {
                  goToNext(nextSibling);
                } else {
                  goto(`#/item/${itemID}`);
                }
              },
              onError: (msg) => {
                clearWatchdog();
                dbg(`onError: ${msg || '(empty)'}`);
                error = msg || 'AVPlay failed (no error message from firmware).';
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
          attachVideoListeners(video, startMs);
        }
      } catch (e) {
        if (e instanceof Unauthorized) goto('#/login');
        else if (e instanceof ApiError && e.code === 'PARENTAL_LIMIT') {
          // Transcode start refused by the parental watch limit.
          error = parentalBlockMessage(e.message);
          loading = false;
        } else {
          error = (e as Error).message;
          loading = false;
        }
      }
    })();

    return () => {
      disableAvplayCompositing();
      destroyLoadingEl();
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

<!-- Music / audiobook view rendered at route root, OUTSIDE the
     .player container. The video-player overlays and AVPlay anchor
     were ending up between this and the user; pulling it up to a
     direct child of the route's template fragment means nothing
     can clip or cover the now-playing screen. -->
{#if isAudioItem && !loading && !error && item}
  <div class="music-view">
    <div class="music-content">
      {#if item.poster_path}
        <img class="music-art" src={api.assetUrl(`/artwork/${item.poster_path}?w=720`)} alt="" />
      {:else}
        <div class="music-art music-art-placeholder">♪</div>
      {/if}
      <div class="music-title">{item.title}</div>
      {#if artistTitle || albumTitle}
        <div class="music-subtitle">
          {#if artistTitle}<span class="music-artist">{artistTitle}</span>{/if}
          {#if artistTitle && albumTitle}<span class="music-dot">·</span>{/if}
          {#if albumTitle}<span class="music-album">{albumTitle}</span>{/if}
        </div>
      {/if}
      <div class="music-meta">
        {#if queuePosition > 0 && queueTotal > 0}
          <span>Track {queuePosition} of {queueTotal}</span>
        {:else if item.year}
          <span>{item.year}</span>
        {/if}
        <span>{paused ? '❚❚ Paused' : '▶ Playing'}</span>
      </div>
      <div class="music-bar">
        <div class="music-elapsed">{fmt(position)}</div>
        <div class="music-track">
          <div class="music-fill" style="width: {progressPct}%"></div>
          {#each chapters as ch (ch.start_ms)}
            {#if duration > 0}
              <div class="music-chapter-marker" style="left: {(ch.start_ms / duration) * 100}%"></div>
            {/if}
          {/each}
        </div>
        <div class="music-remaining">-{fmt(duration - position)}</div>
      </div>
      <div class="music-hints">
        <span>OK play / pause</span>
        <span>← → seek 10s</span>
        {#if item.type === 'track'}
          <span>◀◀ ▶▶ prev / next track</span>
        {:else}
          <span>◀◀ ▶▶ seek 30s</span>
        {/if}
        {#if chapters.length > 0}<span>red/green chapters</span>{/if}
        <span>back exit</span>
      </div>
    </div>
  </div>
{/if}

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="player" onmousemove={showControls}>
  <!-- AVPlay anchor lives directly under <body> — created in onMount.
       Do not place it inside .player; ancestor overflow:hidden /
       stacking contexts can clip the firmware's chroma reservation. -->

  <!-- svelte-ignore a11y_media_has_caption -->
  <!-- HTML5 <video> drives the dev fallback (vite preview) and the
       audio-only direct-play path on TV. Hidden when AVPlay is in
       charge (would cover the hardware overlay), AND for audio items
       — audio plays through the element with no visible track, but
       browsers paint a black rect for empty video that would sit on
       top of the music view. -->
  <video bind:this={video} class="video" class:hidden={usingAvPlay || isAudioItem} playsinline></video>

  <!-- Loading overlay is rendered IMPERATIVELY in onMount as a direct
       child of <body> — same pattern Jellyfin uses (jellyfin-web
       src/components/loading/loading.ts). When AVPlay's hardware
       overlay engages, anything Svelte renders inside .player is
       hidden by the firmware compositor; a body-level fixed div with
       a huge z-index sits above the overlay and stays visible. -->
  {#if error}
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

  <!-- Audio + subtitle pickers. Yellow opens audio (HLS re-issues the
       transcode session with the chosen audio_stream_index, preserving
       position); blue opens subtitles (toggles a video.textTracks
       entry on HTML5 dev fallback, AVPlay setSelectTrack on TV). -->
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
       podcasts. Music + audiobook chapters skip this and chain
       silently at EOS so the closing seconds play through. -->
  {#if upNextShown && nextSibling && !loading}
    <div class="up-next">
      <div class="up-next-label">UP NEXT · {upNextCountdown}s</div>
      <div class="up-next-title">{nextSibling.title}</div>
      <div class="up-next-hint">OK to play now · Back to dismiss</div>
    </div>
  {/if}

  {#if !isAudioItem && controlsVisible && !loading && !error}
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
                   sprite via background-position — no canvas, no
                   per-frame image work. Anchored to the track so the
                   percent-based `left` lands accurately on the
                   playhead regardless of the surrounding layout. -->
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
    /* AVPlay renders its video as a hardware overlay BEHIND the webview.
       Setting the player background opaque (#000) blocks that overlay
       so the user sees a black webview chrome over a playing video.
       Use transparent here and clear body / .tv-root on this route via
       :global() below so the firmware compositor can show through. */
    background: transparent;
    overflow: hidden;
  }

  /* HTML, body, and the layout's .tv-root all paint the dark page
     background everywhere else, but on the watch route those layers
     must let the AVPlay hardware overlay through. Even one opaque
     layer in the chain hides the entire video. Scoped via :global()
     because Svelte's scoped CSS doesn't reach outside this component.
     The html selector targets the topmost paint surface; body still
     gets cleared too so we don't depend on browser stacking quirks. */
  :global(html.player-route),
  :global(body.player-route),
  :global(body.player-route .tv-root) {
    background: transparent !important;
  }

  .video {
    width: 100%;
    height: 100%;
    object-fit: contain;
  }

  .video.hidden {
    display: none;
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



  /* ── Music / audiobook view ────────────────────────────────── */
  .music-view {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 100;
    /* Plain flex centering — most widely supported pattern on the
       Tizen webview (Chromium ~76). place-items / inset weren't
       reliably centering on the panel. */
    display: flex;
    align-items: center;
    justify-content: center;
    background: linear-gradient(180deg, #0d0d18 0%, #07070d 100%);
    color: var(--text-primary);
  }
  .music-content {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 28px;
    width: 1400px;
    max-width: 90%;
  }

  .music-art {
    width: 460px;
    height: 460px;
    object-fit: cover;
    border-radius: 18px;
    background: var(--bg-elevated);
    box-shadow: 0 20px 60px rgba(0, 0, 0, 0.6);
  }
  .music-art-placeholder {
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 180px;
    color: var(--text-muted);
  }
  .music-title {
    font-size: var(--font-2xl);
    font-weight: 600;
    text-align: center;
    max-width: 1400px;
    line-height: 1.2;
  }
  .music-subtitle {
    display: flex;
    align-items: baseline;
    justify-content: center;
    flex-wrap: wrap;
    gap: 14px;
    font-size: var(--font-lg);
    color: var(--text-secondary);
    text-align: center;
    margin-top: -8px;
  }
  .music-artist { color: var(--text-primary); font-weight: 500; }
  .music-album { font-style: italic; }
  .music-dot { color: var(--text-muted); }

  .music-meta {
    display: flex;
    gap: 24px;
    font-size: var(--font-md);
    color: var(--text-secondary);
  }
  .music-bar {
    display: grid;
    grid-template-columns: 120px 1fr 120px;
    align-items: center;
    gap: 28px;
    width: 100%;
    color: var(--text-primary);
    font-size: var(--font-md);
    font-variant-numeric: tabular-nums;
  }
  .music-elapsed {
    text-align: right;
    color: var(--text-secondary);
  }
  .music-remaining {
    text-align: left;
    color: var(--text-secondary);
  }
  .music-track {
    position: relative;
    height: 10px;
    background: rgba(255, 255, 255, 0.18);
    border-radius: 5px;
    overflow: visible;
  }
  .music-fill {
    height: 100%;
    background: var(--accent);
    border-radius: 5px;
  }
  .music-chapter-marker {
    position: absolute;
    top: -5px;
    width: 2px;
    height: 20px;
    background: rgba(255, 255, 255, 0.6);
    transform: translateX(-1px);
  }
  .music-hints {
    display: flex;
    gap: 32px;
    font-size: var(--font-sm);
    color: var(--text-muted);
    margin-top: 8px;
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

  /* Sprite-cropped trickplay preview. Anchored to .track (which is
     position: relative) — `left: <pct>` places the preview's left
     edge at the playhead, then `margin-left: -w/2` centres it.
     `bottom: 32px` lifts it clear of the track. Sprite cropping is
     pure CSS: background-image is the full sprite sheet,
     background-position shifts to the cue's xywh origin, the
     element's size masks the rest. */
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
  .picker-row-error {
    color: #fca5a5;
  }
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
  .picker-row.active .online-sub-lang {
    color: white;
  }
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
