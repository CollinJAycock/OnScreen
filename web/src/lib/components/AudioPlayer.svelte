<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { get } from 'svelte/store';
  import { audio, currentTrack, nextTrack, type AudioTrack } from '$lib/stores/audio';
  import { itemApi, getApiBase, getBearerToken, assetUrl, getClientName } from '$lib/api';
  import { playbackStops } from '$lib/stores/notifications';
  import { toast } from '$lib/stores/toast';
  import {
    adminStopText,
    isPlaybackStoppedError,
    isStopForPlayer,
    probePlaybackStopped,
  } from '$lib/playback-stop';
  import {
    isTauri,
    audioPlayUrl,
    audioPreloadUrl,
    audioPause,
    audioResume,
    audioSeek,
    stopAudio,
    audioState,
    onMediaKey,
    notifyNowPlaying,
    nowPlayingSetMetadata,
    nowPlayingSetPlayback,
    nowPlayingClear,
    replayGainSetMode,
    replayGainSetPreamp,
    audioSetExclusiveMode,
    audioSetVolume,
    type ReplayGainMode,
  } from '$lib/native';
  import { nativeEngine } from '$lib/stores/nativeEngine';
  import { replayGainSettings, setReplayGainMode, setReplayGainPreamp } from '$lib/stores/replayGain';
  import {
    replayGainFromFile,
    replayGainLinear,
    selectReplayGain,
    PREAMP_MIN_DB,
    PREAMP_MAX_DB,
    type ReplayGainInfo,
  } from '$lib/replaygain';
  import { MediaGainRouter, webAudioSupported } from '$lib/webAudioGain';
  import { createSleepTimer, formatRemaining, type SleepTimerMode } from '$lib/stores/sleepTimer';
  import { bookmarkAdded } from '$lib/stores/bookmarks';
  import {
    RATE_PRESETS,
    chapterAt,
    clampRate,
    formatPosition,
    formatRate,
    listeningUnavailable,
    nextChapterBoundary,
    nextChapterStart,
    prevChapterStart,
  } from '$lib/audiobook';

  // Two audio elements rotated for gapless playback. `audioElA` and
  // `audioElB` swap roles every track: when one is "active" (playing
  // the current track), the other is "preload" (idle, with the next
  // track's bytes already in the browser cache + codec init done).
  // On `ended` we swap roles synchronously — sub-frame transition
  // instead of the ~250 ms a fresh src= + decode-init costs.
  let audioElA: HTMLAudioElement;
  let audioElB: HTMLAudioElement;
  let activeIsA = true;

  // Helper accessors instead of reactive `$:` aliases — Svelte's
  // dependency tracker would see `audioEl` mutations elsewhere in
  // the file (audioEl.src = ...) and flag a cycle. Plain functions
  // sidestep the reactivity entirely.
  function activeEl(): HTMLAudioElement { return activeIsA ? audioElA : audioElB; }
  function preloadEl(): HTMLAudioElement { return activeIsA ? audioElB : audioElA; }

  let loadedSrc = '';
  let preloadSrc = '';
  let durationMS = 0;
  let positionMS = 0;
  let scrubbing = false;
  let scrubMS = 0;
  let volume = 1;
  let muted = false;

  // pendingSeekMS holds a resume target captured when a track is
  // loaded with audio.play(queue, idx, startMS) > 0. The element
  // can't honor `currentTime = X` reliably until loadedmetadata
  // fires (browsers silently clamp pre-metadata seeks to 0). We
  // stash the target here and apply it in onLoadedMeta. The audiobook
  // detail page is currently the only caller — music albums always
  // start at 0.
  let pendingSeekMS: number | null = null;

  let track: AudioTrack | null = null;
  let upcoming: AudioTrack | null = null;
  let playing = false;
  let shuffle = false;
  let repeat: 'off' | 'one' | 'all' = 'off';

  // Native audio engine routing — when enabled in Tauri, FLAC tracks
  // bypass <audio> entirely and stream through the Rust cpal+claxon
  // pipeline. The two paths are mutually exclusive: when nativeActive
  // is true we never set src on the <audio> elements (they stay
  // silent), and when false the native engine is stopped so it
  // doesn't compete with the browser audio.
  let useNativeEngine = false;
  const unsubE = nativeEngine.subscribe((v) => { useNativeEngine = v; });
  // The "is native actually doing the playing right now" flag is
  // composed of three things: opt-in preference, in Tauri, and a
  // track is loaded. Cached as a function so the reactive blocks
  // below can branch on it without repeated lookups.
  function nativeActive(): boolean {
    if (!useNativeEngine || !isTauri() || !track) return false;
    // Audiobooks stay on <audio>: listening speed needs the browser's
    // pitch-preserving time-stretch, which the engine doesn't have.
    if (track.audiobook) return false;
    // If the engine rejected this exact URL, treat it as inactive so
    // the <audio> fallback below takes over. Browsers handle the long
    // tail of weird WAV/AIFF/codec variants that symphonia refuses.
    const base = getApiBase().replace(/\/api\/v1\/?$/, '');
    const desired = `${base}/media/stream/${track.fileId}`;
    return nativeFailedUrl !== desired;
  }
  // Tracks the URL the engine was last asked to play so we don't
  // re-call audio_play_url on irrelevant reactive triggers (volume
  // changes, position scrubs, etc.). Mirrors the loadedSrc field
  // used by the <audio> path.
  let nativeLoadedUrl = '';
  // The most recent URL the native engine rejected (probe failure,
  // unsupported codec, etc.). Sticky for the lifetime of the track —
  // when the queue advances to a new track this gets cleared via the
  // track-change branch below. Without it, the catch handler's
  // nativeLoadedUrl reset would tight-loop the IPC on every failure.
  let nativeFailedUrl = '';
  // Polling handle for the engine→UI sync (position display +
  // auto-advance on EOS). 250 ms is the same cadence the existing
  // `<audio>` `timeupdate` event fires at — keeps the seek bar
  // ticking without churning subscribers per frame.
  let nativePollHandle: ReturnType<typeof setInterval> | null = null;

  // ── ReplayGain in the browser (Web Audio) ──────────────────────────
  // Desktop builds apply ReplayGain inside the native engine (the
  // replayGainSet* IPC in onMount) and never enter this path — rgRouter
  // is null under Tauri. In a browser, each <audio> element is tapped
  // into its own GainNode on one shared AudioContext ($lib/webAudioGain)
  // — but only once the user has switched ReplayGain on. With it off
  // (the default) nothing is tapped and playback is exactly as before.
  let rgMode: ReplayGainMode = 'off';
  let rgPreampDb = 0;
  const unsubRg = replayGainSettings.subscribe((s) => {
    rgMode = s.mode;
    rgPreampDb = s.preampDb;
  });
  const rgRouter: MediaGainRouter | null =
    typeof window !== 'undefined' && !isTauri() && webAudioSupported() ? new MediaGainRouter() : null;
  // Why the Web Audio path gave up for this session ('' = it didn't).
  let rgDisabledReason = '';
  // Bumped when elements get tapped so the level block re-runs.
  let rgGraphVersion = 0;
  // fileId -> tags, for tracks queued without `replayGain` (the transfer
  // receiver, older callers). Looked up via /items/{id} before the track
  // starts — for the next track while the current one is still playing.
  let rgCache = new Map<string, ReplayGainInfo>();
  const rgLookups = new Map<string, Promise<void>>();
  // fileId whose prerequisites (graph decision + tags) have resolved.
  let rgReadyFor = '';
  const rgPreparing = new Set<string>();
  let rgMenuOpen = false;
  let rgWrapEl: HTMLDivElement | null = null;
  const RG_MODES: { value: ReplayGainMode; label: string }[] = [
    { value: 'off', label: 'Off' },
    { value: 'track', label: 'Track' },
    { value: 'album', label: 'Album' },
  ];

  $: rgWanted = !!rgRouter && rgMode !== 'off' && rgDisabledReason === '';

  function rgTagsFor(t: AudioTrack | null, cache: Map<string, ReplayGainInfo>): ReplayGainInfo | undefined {
    if (!t) return undefined;
    return t.replayGain ?? cache.get(t.fileId);
  }

  function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('timeout')), ms);
      p.then(
        (v) => { clearTimeout(timer); resolve(v); },
        (e) => { clearTimeout(timer); reject(e); },
      );
    });
  }

  function lookupRg(t: AudioTrack): Promise<void> {
    if (t.replayGain !== undefined || rgCache.has(t.fileId)) return Promise.resolve();
    const fileId = t.fileId;
    let p = rgLookups.get(fileId);
    if (!p) {
      p = (async () => {
        let info: ReplayGainInfo = {};
        try {
          const detail = await withTimeout(itemApi.get(t.id), 2500);
          const f = detail.files?.find((x) => x.id === fileId) ?? detail.files?.[0];
          info = replayGainFromFile(f);
        } catch {
          // Lookup failed or timed out — play at unity rather than hold
          // the track hostage to a slow metadata call.
        }
        rgCache.set(fileId, info);
        rgCache = rgCache;
        rgLookups.delete(fileId);
      })();
      rgLookups.set(fileId, p);
    }
    return p;
  }

  function rgBothRouted(): boolean {
    return !!rgRouter && !!audioElA && !!audioElB && rgRouter.isRouted(audioElA) && rgRouter.isRouted(audioElB);
  }

  // Fast path so a gapless hand-off never waits a microtask: graph up and
  // the next track's tags already fetched during preload.
  function rgFastReady(t: AudioTrack | null, cache: Map<string, ReplayGainInfo>, _graph: number): boolean {
    return !!t && !!rgRouter && rgRouter.running && rgBothRouted() && rgTagsFor(t, cache) !== undefined;
  }

  // Resolve everything the active track's gain depends on: whether the
  // elements can be tapped at all (context running, media same-origin)
  // and the track's tags. The play block holds el.play() until this lands
  // so a track never starts at unity and jumps to its gain mid-note.
  async function prepareRg(t: AudioTrack) {
    const key = t.fileId;
    if (!rgRouter || rgPreparing.has(key)) return;
    rgPreparing.add(key);
    try {
      const [routeOk] = await Promise.all([
        rgBothRouted() && rgRouter.running
          ? Promise.resolve(true)
          : rgRouter.canRoute(assetUrl(`/media/stream/${key}`)),
        lookupRg(t),
      ]);
      if (routeOk && audioElA && audioElB && !rgBothRouted()) {
        // Tap BOTH elements now, the preload one included, so a gapless
        // hand-off never builds graph nodes at the track boundary. The
        // initial gain is the element's current volume; applyLevels()
        // below (same task) pins element.volume to 1 and sets the real
        // gain, so an element that's already playing doesn't blip.
        rgRouter.attach(audioElA, audioElA.volume);
        rgRouter.attach(audioElB, audioElB.volume);
        rgGraphVersion += 1;
      }
    } finally {
      rgPreparing.delete(key);
      rgDisabledReason = rgRouter.disabledReason;
      applyLevels(activeIsA, volume, muted, rgMode, rgPreampDb, track, rgCache, rgGraphVersion);
      if (track?.fileId === key) rgReadyFor = key;
    }
  }

  // Unlock the AudioContext from real user gestures — the click that
  // starts an album on another page arrives here first (capture phase),
  // so the context is created inside the gesture the autoplay policy
  // requires. No-op while ReplayGain is off.
  function onRgGesture() {
    if (rgRouter && rgMode !== 'off') rgRouter.unlock();
  }

  function chooseRgMode(m: ReplayGainMode) {
    setReplayGainMode(m);
    // This click is itself a gesture — start the graph now so the
    // current track picks the gain up immediately.
    if (m !== 'off') rgRouter?.unlock();
  }

  function onRgPreampInput(e: Event) {
    const v = parseFloat((e.target as HTMLInputElement).value);
    if (Number.isFinite(v)) setReplayGainPreamp(v);
  }

  function onRgWindowClick(e: MouseEvent) {
    if (rgMenuOpen && rgWrapEl && !rgWrapEl.contains(e.target as Node)) rgMenuOpen = false;
  }

  function onRgKeydown(e: KeyboardEvent) {
    if (rgMenuOpen && e.key === 'Escape') rgMenuOpen = false;
  }

  function fmtDb(db: number, digits = 1): string {
    const s = Math.abs(db).toFixed(digits);
    if (Number(s) === 0) return `${s} dB`;
    return `${db > 0 ? '+' : '−'}${s} dB`;
  }

  // What the menu reports for the current track.
  $: rgCurrentTags = rgTagsFor(track, rgCache);
  $: rgCurrentSel = selectReplayGain(rgCurrentTags, rgMode);
  $: rgAppliedDb = 20 * Math.log10(replayGainLinear(rgCurrentTags, rgMode, rgPreampDb));

  // ── Audiobook listening: speed, bookmarks, sleep timer ─────────────
  // Speed is per book, saved on the server (GET/PUT
  // /items/{id}/playback-rate), and offered only for audiobooks — music
  // always plays at 1×. A book's chapters share its speed, so it's fetched
  // once per book, not per chapter. applyRate() sets it on BOTH elements,
  // as defaultPlaybackRate too: the preload element then starts the next
  // chapter at speed on a gapless swap, and a fresh src= load (which resets
  // playbackRate to the default) keeps it.
  let rate = 1;
  let rateBook = '';      // the book `rate` is for; '' when not an audiobook
  let rateLoad = 0;       // bumped per book so a late answer for the last one is dropped
  let rateChosen = false; // the listener picked a speed before the saved one arrived
  // False when the server has no listening routes (an older server) or
  // won't show this book: the bookmark control hides, speed stays local.
  let listeningApi = true;

  function syncBook(t: AudioTrack | null) {
    const book = t?.audiobook?.bookId ?? '';
    if (book === rateBook) return;
    rateBook = book;
    rate = 1;
    rateChosen = false;
    listeningApi = true;
    const load = ++rateLoad;
    if (!t || !book) return;
    itemApi.getPlaybackRate(t.id).then(
      (r) => { if (load === rateLoad && !rateChosen) rate = clampRate(r.rate); },
      (e) => { if (load === rateLoad && listeningUnavailable(e)) listeningApi = false; },
    );
  }

  // Applies at once. The save is fire-and-forget: a failed PUT doesn't
  // undo what the listener hears.
  function chooseRate(r: number) {
    rate = clampRate(r);
    rateChosen = true;
    openMenu = '';
    if (track?.audiobook) void itemApi.setPlaybackRate(track.id, rate).catch(() => {});
  }

  // One listening popover open at a time; a click outside closes it.
  let openMenu: '' | 'speed' | 'bookmark' | 'sleep' = '';
  let speedWrapEl: HTMLDivElement | null = null;
  let bookmarkWrapEl: HTMLDivElement | null = null;
  let sleepWrapEl: HTMLDivElement | null = null;

  function toggleMenu(m: 'speed' | 'sleep') {
    openMenu = openMenu === m ? '' : m;
  }

  function onMenuWindowClick(e: MouseEvent) {
    const wrap = openMenu === 'speed' ? speedWrapEl
      : openMenu === 'bookmark' ? bookmarkWrapEl
      : openMenu === 'sleep' ? sleepWrapEl
      : null;
    if (wrap && !wrap.contains(e.target as Node)) openMenu = '';
  }

  // Add bookmark. The position is taken when the prompt opens, so typing
  // a note while the book plays on still marks the moment you meant. The
  // new bookmark goes on the playable item (this chapter, or the
  // single-file book) and is announced to an open audiobook page.
  let bookmarkMS = 0;
  let bookmarkNote = '';
  let bookmarkSaving = false;

  function openBookmark() {
    if (openMenu === 'bookmark') {
      openMenu = '';
      return;
    }
    bookmarkMS = positionMS;
    bookmarkNote = '';
    openMenu = 'bookmark';
  }

  async function saveBookmark() {
    const t = track;
    if (!t?.audiobook || bookmarkSaving) return;
    bookmarkSaving = true;
    try {
      const b = await itemApi.addBookmark(t.id, Math.max(0, Math.round(bookmarkMS)), bookmarkNote.trim());
      bookmarkAdded.set({ bookId: t.audiobook.bookId, bookmark: b });
      if (openMenu === 'bookmark') openMenu = '';
      toast.success(`Bookmark added at ${formatPosition(b.position_ms)}`);
    } catch (e) {
      toast.error(listeningUnavailable(e)
        ? "Bookmarks aren't available for this book"
        : e instanceof Error ? e.message : 'Could not add the bookmark');
    } finally {
      bookmarkSaving = false;
    }
  }

  function focusOnMount(node: HTMLElement) {
    node.focus();
  }

  // Sleep timer, for music too — the player's own, not the watch page's.
  // "End of chapter" pauses at sleepAtMS, the next embedded chapter
  // boundary in a single-file book (re-taken after a seek or a track
  // change), or, when that's null, at the end of the current track: a
  // chapter file, or a song ("End of track").
  const sleep = createSleepTimer();
  let sleepAtMS: number | null = null;
  const SLEEP_OPTIONS: { mode: SleepTimerMode; label: string }[] = [
    { mode: '15m', label: '15 minutes' },
    { mode: '30m', label: '30 minutes' },
    { mode: '45m', label: '45 minutes' },
    { mode: '60m', label: '1 hour' },
  ];

  $: sleepUnit = track?.audiobook ? 'chapter' : 'track';

  function pickSleep(mode: SleepTimerMode) {
    openMenu = '';
    if (mode === 'off') {
      sleep.cancel();
      sleepAtMS = null;
      return;
    }
    sleep.start(mode, onSleepFire);
    sleepAtMS = mode === 'chapter' ? nextChapterBoundary(track?.audiobook?.chapters, positionMS) : null;
    const opt = SLEEP_OPTIONS.find((o) => o.mode === mode);
    toast.success(opt ? `Sleep timer set for ${opt.label}` : `Sleep timer set — will pause at the end of this ${sleepUnit}`);
  }

  // A duration timer ran out: pause, keep the player open.
  function onSleepFire() {
    sleepAtMS = null;
    if (!track) return;
    if (audioElA && audioElB && !activeEl().paused) activeEl().pause();
    audio.pause();
    toast.success('Sleep timer ended — playback paused');
  }

  // End of chapter inside a single-file book: pause on the boundary and
  // park there, so Play starts the next chapter from its first word.
  function sleepAtChapterEnd(at: number) {
    sleep.cancel();
    sleepAtMS = null;
    const el = activeEl();
    el.pause();
    el.currentTime = at / 1000;
    positionMS = at;
    audio.setPosition(at);
    audio.pause();
    toast.success('Sleep timer ended — paused at the end of the chapter');
  }

  // A track ended on its own. With "end of chapter" armed the queue still
  // moves on — so Play continues with the next chapter rather than
  // replaying this one — but paused.
  function advanceAfterEnd() {
    if ($sleep.mode === 'chapter') {
      const unit = sleepUnit;
      sleep.cancel();
      sleepAtMS = null;
      audio.nextPaused();
      toast.success(`Sleep timer ended — paused at the end of the ${unit}`);
      return;
    }
    audio.next();
  }

  // What the sleep button shows while armed: the countdown, or for "end of
  // chapter" the listening time left to the boundary at the current speed
  // (null while the end isn't known yet).
  function sleepLeft(mode: SleepTimerMode, remaining: number, at: number | null, pos: number, dur: number, r: number): number | null {
    if (mode !== 'chapter') return remaining;
    const end = at ?? (dur > 0 ? dur : null);
    return end === null ? null : Math.max(0, (end - pos) / r);
  }
  $: sleepLeftMS = sleepLeft($sleep.mode, $sleep.remainingMs, sleepAtMS, positionMS, durationMS, rate);

  // A single-file book's current chapter, named in the player.
  $: currentChapter = chapterAt(track?.audiobook?.chapters, positionMS);

  // Scrobble cadence — report `playing` every 10s so Continue Watching reflects
  // current position without flooding the API. Pause/stop are reported immediately.
  let lastReportedMS = 0;
  let lastReportedID = '';

  // The store's seekSeq this player has acted on (see seekTo).
  let appliedSeekSeq = 0;
  const unsubA = audio.subscribe((s) => {
    const wasPlaying = playing;
    playing = s.playing;
    shuffle = s.shuffle;
    repeat = s.repeat;
    // Edge: paused — report a discrete pause event for the active track.
    if (wasPlaying && !s.playing && track) {
      void report('paused');
    }
    // audio.seek() or Previous restarting the track: move the element.
    // (A cleared store starts the count again, which is not a seek.)
    if (s.seekSeq !== appliedSeekSeq) {
      const requested = s.seekSeq > appliedSeekSeq;
      appliedSeekSeq = s.seekSeq;
      if (requested && track) seekTo(s.positionMS);
    }
  });
  let prevTrack: AudioTrack | null = null;
  // Set by the EOS paths (onEnded / native decoder EOS) right before they
  // advance the queue: they've already reported the finished track stopped at
  // full duration, so the track-change handler below must NOT report it a
  // second time. Without this guard every naturally-completed track emits two
  // 'stop' watch-events — a duplicate scrobble and a redundant matview write.
  let endedTrackID: string | null = null;
  const unsubT = currentTrack.subscribe((t) => {
    // When the active track changes, mark the previous one stopped so it leaves
    // any "Now Playing" surfaces immediately. Skip it when EOS already reported
    // this track (natural completion) — only a manual skip mid-track needs the
    // stop emitted here, at the actual cut-off position.
    if (prevTrack && (!t || prevTrack.id !== t.id)) {
      if (prevTrack.id === endedTrackID) {
        endedTrackID = null;
      } else {
        void itemApi
          .progress(prevTrack.id, positionMS, durationMS || (prevTrack.durationMS ?? 0), 'stopped')
          .catch(() => {});
      }
    }
    // OS notification on track change — only when actually moving to
    // a *different* track (skipping initial track-load on launch
    // would feel like spam) and only when the user can't already see
    // the new track in the focused window. Tauri's window focus check
    // would be more precise but document.hasFocus() is cheap and
    // covers the common case of "user switched apps mid-album."
    if (t && prevTrack && prevTrack.id !== t.id && !document.hasFocus()) {
      const subtitle = [t.artist, t.album].filter(Boolean).join(' — ') || 'Now playing';
      void notifyNowPlaying(t.title, subtitle);
    }
    // OS now-playing widget — fires on every track change, focused or
    // not (the widget is the persistent lockscreen / Bluetooth display,
    // not a transient toast). The art URL is sent *without* `?token=`;
    // the bearer rides in a separate field so the Rust side can fetch
    // with an Authorization header and write the bytes to a local
    // cache file. We hand the OS shell a `file://` URI so the bearer
    // never enters Windows SMTC / macOS NowPlayingInfoCenter / MPRIS
    // caches.
    if (t && (!prevTrack || prevTrack.id !== t.id)) {
      const base = getApiBase().replace(/\/api\/v1\/?$/, '');
      const artUrl = t.posterPath
        ? `${base}/artwork/${encodeURI(t.posterPath)}?w=600`
        : null;
      void nowPlayingSetMetadata({
        title: t.title,
        artist: t.artist ?? null,
        album: t.album ?? null,
        artUrl,
        artBearer: artUrl ? getBearerToken() : null,
        durationMs: t.durationMS ?? null,
      });
    }
    // currentTrack is derived from the whole audio store, and Svelte treats
    // objects as always-changed, so this callback also runs on every
    // setPosition tick. Only a real track change may reset the 10 s progress
    // throttle — resetting it per tick sent a 'playing' report ~4×/second.
    const trackChanged = (prevTrack?.id ?? null) !== (t?.id ?? null);
    prevTrack = t;
    track = t;
    if (trackChanged) {
      lastReportedMS = 0;
      lastReportedID = '';
      syncBook(t);
      // The timer follows the player: closing it cancels the timer, and a
      // new track re-takes where "end of chapter" is.
      if (!t) {
        sleep.cancel();
        sleepAtMS = null;
      } else if ($sleep.mode === 'chapter') {
        sleepAtMS = nextChapterBoundary(t.audiobook?.chapters, get(audio).positionMS);
      }
    }
  });
  const unsubN = nextTrack.subscribe((n) => {
    upcoming = n;
  });

  async function report(state: 'playing' | 'paused' | 'stopped') {
    if (!track) return;
    const t = track;
    try {
      await itemApi.progress(t.id, positionMS, durationMS || (t.durationMS ?? 0), state);
      lastReportedMS = positionMS;
      lastReportedID = t.id;
    } catch (e) {
      // An admin stopped this stream and the playback.stop event didn't reach
      // us (SSE down, another tab): the server refuses the 'playing' beat
      // with 403 PLAYBACK_STOPPED. Anything else is offline — drop the event.
      if (isPlaybackStoppedError(e) && track === t) handleAdminStop(e.message);
    }
  }

  // Admin "stop this stream" (Now Playing → Stop) for music. The same user's
  // playback.stop SSE event reaches every open player, so only stop when it
  // targets this track (and this client, when the event names one). The
  // store replays its last value on subscribe; skip that so an old stop
  // can't end a fresh listen.
  let playbackStopsPrimed = false;
  const unsubStop = playbackStops.subscribe((evt) => {
    if (!playbackStopsPrimed || !evt || !track) return;
    if (!isStopForPlayer(evt, { itemId: track.id, sessionId: null, clientName: getClientName() })) return;
    handleAdminStop(adminStopText(evt.message));
  });
  playbackStopsPrimed = true;

  // Stop music because an admin stopped it: halt the element now, drop the
  // queue (the track-change subscriber reports the cut-off position as
  // 'stopped', which the server never refuses) — NOT skip to the next track —
  // and tell the listener why, in a toast since the player bar goes away.
  // Idempotent: the SSE event, a refused beat and a refused range request can
  // all land for one stop.
  function handleAdminStop(text: string) {
    if (!track) return;
    stopNativePolling();
    if (audioElA && audioElB && !activeEl().paused) activeEl().pause();
    audio.clear();
    toast.info(text, 15000);
  }

  // OS media-key listener handle. Registered on mount, torn down
  // on destroy. No-op in the browser bundle.
  let mediaKeyUnlisten: (() => void) | null = null;

  // Restore volume from localStorage so it persists across reloads.
  onMount(() => {
    const v = localStorage.getItem('onscreen_audio_volume');
    if (v !== null) {
      const n = parseFloat(v);
      if (!Number.isNaN(n)) volume = Math.max(0, Math.min(1, n));
    }
    const m = localStorage.getItem('onscreen_audio_muted');
    if (m === '1') muted = true;

    // Restore native-engine ReplayGain settings. The Rust atomics
    // reset to defaults on every process launch; the user's choice
    // lives in localStorage and we re-push it on mount so the
    // settings survive an app restart. No-op in browser builds via
    // the isTauri() guard inside replayGainSet*.
    const storedRgMode = localStorage.getItem('onscreen_native_rg_mode');
    if (storedRgMode === 'off' || storedRgMode === 'track' || storedRgMode === 'album') {
      void replayGainSetMode(storedRgMode as ReplayGainMode);
    }
    const storedRgPreamp = parseFloat(localStorage.getItem('onscreen_native_rg_preamp') ?? '');
    if (Number.isFinite(storedRgPreamp)) {
      void replayGainSetPreamp(storedRgPreamp);
    }
    const storedExclusive = localStorage.getItem('onscreen_native_exclusive') === '1';
    if (storedExclusive) {
      void audioSetExclusiveMode(true);
    }

    if (rgRouter) {
      window.addEventListener('pointerdown', onRgGesture, true);
      window.addEventListener('keydown', onRgGesture, true);
      window.addEventListener('click', onRgGesture, true);
    }

    // Wire OS media keys → audio store. The Rust side registers the
    // shortcuts globally so they fire whether or not OnScreen is
    // focused. Guards against double-firing while no track is
    // loaded — pressing play-pause with an empty queue does nothing
    // rather than getting stuck in a paused-but-empty state.
    void onMediaKey((action) => {
      switch (action) {
        case 'play-pause':
          if (track) audio.togglePlay();
          break;
        case 'next':
          skipNext();
          break;
        case 'previous':
          skipPrev();
          break;
        case 'stop':
          audio.clear();
          break;
      }
    }).then((unlisten) => {
      mediaKeyUnlisten = unlisten;
    });
  });

  onDestroy(() => {
    unsubA(); unsubT(); unsubN(); unsubE(); unsubRg(); unsubStop();
    sleep.cancel();
    if (rgRouter) {
      window.removeEventListener('pointerdown', onRgGesture, true);
      window.removeEventListener('keydown', onRgGesture, true);
      window.removeEventListener('click', onRgGesture, true);
    }
    if (nativePollHandle) clearInterval(nativePollHandle);
    if (mediaKeyUnlisten) mediaKeyUnlisten();
    // Stop the native engine on player destroy so it doesn't keep
    // playing after navigation away from a route that owns the
    // AudioPlayer (currently only the root layout owns it, but keep
    // the cleanup safe for future component-scoped reuse).
    if (isTauri()) {
      void stopAudio();
      void nowPlayingClear();
    }
  });

  // Native engine → UI sync. Polls audio_state every 250 ms while
  // native playback is active so the seek bar ticks and auto-advance
  // fires on EOS. Skipped when nativeActive is false so we don't
  // burn an interval timer in browser builds. The poll cadence is
  // intentionally the same as `<audio>` timeupdate — we want the
  // two paths to feel identical to the user.
  function startNativePolling() {
    if (nativePollHandle) return;
    nativePollHandle = setInterval(async () => {
      try {
        const s = await audioState();
        if (!s.playing) {
          // Engine stopped on its own (most likely a play error
          // before any sample landed). Drop the poll loop and let
          // the next track-change reactive block start a fresh one.
          stopNativePolling();
          return;
        }
        // Push position into the store so the seek bar + scrobble
        // logic see the same number the <audio> path would emit.
        // Skip while scrubbing so we don't fight the user's drag.
        if (!scrubbing) {
          positionMS = s.position_ms;
          audio.setPosition(positionMS);
        }
        if (s.ended) {
          // Decoder hit EOS. Mark the finished track stopped at full
          // duration (matches the <audio> onEnded path) and advance.
          // The advance triggers a track change, which kicks off a
          // new audioPlayUrl call — by the time the engine sees the
          // new source, ended is back to false.
          if (track) {
            const d = durationMS || (track.durationMS ?? 0);
            endedTrackID = track.id;
            void itemApi.progress(track.id, d, d, 'stopped').catch(() => {});
          }
          stopNativePolling();
          advanceAfterEnd();
        }
      } catch {
        // IPC failure — most likely the engine is between tracks.
        // Polling resumes on the next tick.
      }
    }, 250);
  }

  function stopNativePolling() {
    if (nativePollHandle) {
      clearInterval(nativePollHandle);
      nativePollHandle = null;
    }
  }

  // Native engine routing. Mirrors the <audio> reactive blocks but
  // calls the Rust IPC instead of touching DOM elements. Runs first
  // so on a track change we kick off the engine before the <audio>
  // block decides whether to attach src — and the <audio> block
  // skips its work entirely when nativeActive() is true.
  $: if (track && nativeActive()) {
    // Resolve the absolute URL the Rust ureq client can fetch. The
    // api.ts apiBase is either same-origin "/api/v1" (browser) or
    // "<server>/api/v1" (Tauri). For Tauri it's always absolute so
    // dropping /api/v1 → /media/stream/<id> gives us the right URL.
    const base = getApiBase().replace(/\/api\/v1\/?$/, '');
    const desired = `${base}/media/stream/${track.fileId}`;
    if (nativeFailedUrl === desired) {
      // Native engine already rejected this URL once — the <audio>
      // fallback below handles it. Skip without re-firing the IPC,
      // otherwise we tight-loop spamming the server.
    } else if (nativeLoadedUrl !== desired) {
      nativeLoadedUrl = desired;
      positionMS = 0;
      durationMS = (track.durationMS ?? 0);
      // Pass the bearer so the engine's HTTP fetch can authenticate
      // — same auth as the api.ts wrapper uses for everything else.
      void audioPlayUrl(desired, getBearerToken(), null).then(() => {
        startNativePolling();
      }).catch((err) => {
        console.warn('native engine play failed:', err);
        stopNativePolling();
        // Mark this URL as native-incompatible so the reactive block
        // doesn't re-fire the IPC on the next reactivity tick. The
        // <audio> element below picks it up via the same nativeActive
        // gate (which now checks nativeFailedUrl too) — browsers
        // handle the long tail of weird WAV/AIFF/etc. headers that
        // symphonia rejects.
        nativeFailedUrl = desired;
        nativeLoadedUrl = '';
      });
    }
  } else if (track && nativeFailedUrl !== '') {
    // Track exists but native engine isn't active (toggle off, or
    // the engine rejected this URL). Clear the sticky failure when
    // the track has moved to a different file so a fresh attempt
    // happens on the next track change.
    const base = getApiBase().replace(/\/api\/v1\/?$/, '');
    const desired = `${base}/media/stream/${track.fileId}`;
    if (nativeFailedUrl !== desired) {
      nativeFailedUrl = '';
    }
  } else if (!track && nativeLoadedUrl !== '') {
    nativeLoadedUrl = '';
    stopNativePolling();
    if (isTauri()) {
      void stopAudio();
      void nowPlayingClear();
    }
  }

  // The engine was playing but this track goes through <audio> (an
  // audiobook — see nativeActive — or the engine was switched off): stop
  // it so the two don't play at once.
  $: if (track && !nativeActive() && nativeLoadedUrl !== '') {
    nativeLoadedUrl = '';
    stopNativePolling();
    if (isTauri()) void stopAudio();
  }

  // Pause/resume sync for native playback. The <audio> block below
  // only fires when loadedSrc is set; its no-op path during native
  // playback is correct. This block handles the native equivalent.
  $: if (nativeActive() && nativeLoadedUrl) {
    if (playing) {
      void audioResume();
    } else {
      void audioPause();
    }
  }

  // OS now-playing widget — playback state. Fires on every play /
  // pause transition while a track is loaded. The widget's scrubber
  // is updated from positionMS so the lockscreen seek bar reflects
  // local progress. Stopped state is handled by the explicit
  // nowPlayingClear() calls in stopAudio paths below; this block is
  // play/pause-only.
  $: if (track) {
    void nowPlayingSetPlayback(playing ? 'playing' : 'paused', positionMS);
  }

  // Optimistically preload the next track on the native engine when
  // upcoming changes — same trigger as the existing <audio> preload
  // block but going through the engine's audio_preload_url IPC. The
  // engine spawns a decoder thread + ringbuf so the matching
  // audio_play_url call (when we advance to this track) skips the
  // HTTP + claxon round-trip and the gap between tracks shrinks
  // from ~200-500 ms to whatever cpal's device-activation cost is
  // (~10-20 ms on every host we care about).
  let nativePreloadedUrl = '';
  $: if (nativeActive() && upcoming && track && upcoming.id !== track.id) {
    const base = getApiBase().replace(/\/api\/v1\/?$/, '');
    const desired = `${base}/media/stream/${upcoming.fileId}`;
    if (nativePreloadedUrl !== desired) {
      nativePreloadedUrl = desired;
      void audioPreloadUrl(desired, getBearerToken());
    }
  } else if (nativeActive() && !upcoming) {
    nativePreloadedUrl = '';
  }

  // Swap source when track changes; set src='' when track cleared.
  // Two paths: (1) the new track is what the preload element already
  // has buffered → flip activeIsA, no fresh src= load; (2) it's not
  // (user picked a different track manually, no preload happened in
  // time, etc.) → fall back to loading on the active element.
  // Skipped entirely when the native engine owns playback — the
  // <audio> elements stay silent so they don't double-play.
  $: if (audioElA && audioElB && track && !nativeActive()) {
    // Wrap with assetUrl so cross-origin native builds (when the
    // user has the engine OFF and falls back to <audio>) hit the
    // configured server, not the Tauri webview origin. Browser
    // same-origin builds get the path back unchanged.
    const desired = assetUrl(`/media/stream/${track.fileId}`);
    if (loadedSrc !== desired) {
      // Capture the resume target the store carries (set by
      // audio.play(queue, idx, startMS) — non-zero only for
      // audiobook resume). Applied in onLoadedMeta because pre-
      // metadata seeks get silently clamped to 0 in some browsers.
      const resumeMS = get(audio).positionMS;
      pendingSeekMS = resumeMS > 0 ? resumeMS : null;

      if (preloadSrc === desired) {
        // Gapless path: the next-track element is already primed —
        // flip roles and play. The browser's already done codec init
        // and (usually) buffered the head of the file, so the swap
        // is sub-frame audible-wise.
        activeIsA = !activeIsA;
        loadedSrc = desired;
        preloadSrc = '';
        positionMS = resumeMS;
        durationMS = (track.durationMS ?? 0);
      } else {
        // Cold path: src= load on the active element. Same code as
        // before this change — gapless only kicks in for natural
        // queue advancement.
        loadedSrc = desired;
        const el = activeEl();
        el.src = desired;
        el.currentTime = resumeMS / 1000;
        positionMS = resumeMS;
        durationMS = (track.durationMS ?? 0);
      }
    }
  } else if (audioElA && audioElB && !track && loadedSrc !== '') {
    loadedSrc = '';
    const el = activeEl();
    el.removeAttribute('src');
    el.load();
  }

  // Keep the preload element pointed at the next track. Skipped when
  // upcoming === current (repeat=one) — would just hammer the same
  // file pointlessly. Re-runs whenever the queue changes shape so
  // toggling shuffle / appending / reordering gets reflected.
  // Skipped entirely under native engine — gapless preload there
  // is the engine's responsibility (next commit on the audio track).
  $: if (audioElA && audioElB && upcoming && track && upcoming.id !== track.id && !nativeActive()) {
    const desired = assetUrl(`/media/stream/${upcoming.fileId}`);
    if (preloadSrc !== desired) {
      preloadSrc = desired;
      const el = preloadEl();
      el.src = desired;
      el.currentTime = 0;
      // load() forces the browser to start fetching + codec init now
      // rather than waiting for the first play() call. preload="auto"
      // would do this implicitly but isn't reliable across browsers
      // when the element is currently muted/silent.
      el.load();
    }
  } else if (audioElA && audioElB && !upcoming && preloadSrc !== '') {
    preloadSrc = '';
    const el = preloadEl();
    el.removeAttribute('src');
    el.load();
  }

  // ReplayGain prerequisites for the current track (browser + RG on only),
  // and the next track's tags while this one is still playing so the
  // gapless hand-off already knows its gain.
  $: if (track && rgWanted && audioElA && audioElB) void prepareRg(track);
  $: if (rgWanted && upcoming && upcoming.replayGain === undefined) void lookupRg(upcoming);
  $: rgGate = rgWanted && !!track && rgReadyFor !== track.fileId && !rgFastReady(track, rgCache, rgGraphVersion);

  // Output level per element: user volume/mute x ReplayGain.
  //
  // Untapped elements (ReplayGain off, Tauri, or the Web Audio path
  // unavailable) keep the original behaviour: element.volume carries the
  // user level on the active element, 0 on the preload one.
  //
  // Tapped elements pin element.volume to 1 and carry the whole level in
  // their GainNode. The Web Audio spec says a tapped element's volume must
  // still apply ahead of the tap, and Chrome and Firefox follow it — so
  // slider-on-element.volume + ReplayGain-in-the-GainNode would work
  // there, but it's two gain stages whose product depends on each engine
  // getting that detail right, and iOS WebKit treats element.volume as
  // read-only 1 (the slider would do nothing). One GainNode = one float
  // multiply, identical on every engine; pinning element.volume to 1 means
  // the slider can never be applied twice; mute is gain 0.
  //
  // activeIsA is passed directly (not via activeEl()) so Svelte's dep
  // tracker re-runs this on a gapless swap — otherwise the old preload
  // element would start the next track at volume 0. Declared BEFORE the
  // play block so a track's gain is set before el.play() in the same
  // flush: the new track's first samples already see it.
  function applyLevels(
    aIsActive: boolean,
    vol: number,
    isMuted: boolean,
    mode: ReplayGainMode,
    preampDb: number,
    cur: AudioTrack | null,
    cache: Map<string, ReplayGainInfo>,
    _graph: number,
  ) {
    if (!audioElA || !audioElB) return;
    const active = aIsActive ? audioElA : audioElB;
    const preload = aIsActive ? audioElB : audioElA;
    const level = isMuted ? 0 : vol;
    for (const [el, isActive] of [[active, true], [preload, false]] as const) {
      if (rgRouter?.isRouted(el)) {
        el.volume = 1;
        const rg = isActive ? replayGainLinear(rgTagsFor(cur, cache), mode, preampDb) : 0;
        // Fade only when the element is audible right now; a paused
        // element about to start must have its exact gain from sample 0.
        rgRouter.setGain(el, isActive ? level * rg : 0, !el.paused);
      } else {
        el.volume = isActive ? level : 0;
      }
    }
  }

  $: if (audioElA && audioElB) {
    applyLevels(activeIsA, volume, muted, rgMode, rgPreampDb, track, rgCache, rgGraphVersion);
  }

  // Listening speed on both elements (see the audiobook section above).
  // Re-runs on every source change and gapless swap. preservesPitch keeps a
  // narrator at their own pitch at 1.5×.
  function applyRate(r: number, _aIsActive: boolean, _src: string, _preload: string) {
    for (const el of [audioElA, audioElB]) {
      el.defaultPlaybackRate = r;
      el.playbackRate = r;
      el.preservesPitch = true;
    }
  }

  $: if (audioElA && audioElB) applyRate(rate, activeIsA, loadedSrc, preloadSrc);

  // Mirror playing flag to the element. Browser autoplay policies may reject;
  // catch and pause the store so the UI matches reality. Skipped when
  // native engine is active — its own pause/resume sync block above
  // handles the same flag flip via IPC.
  $: if (audioElA && audioElB && loadedSrc && !nativeActive()) {
    const el = activeEl();
    if (playing && el.paused) {
      // rgGate holds the start until this track's ReplayGain is known
      // (a few ms normally; bounded by the lookup/start timeouts). It is
      // always false with ReplayGain off, so the default path is as before.
      if (!rgGate) {
        if (rgRouter?.isRouted(el)) rgRouter.resume();
        el.play().catch(() => audio.pause());
      }
    } else if (!playing && !el.paused) {
      el.pause();
    }
  }

  // Mirror volume to the native engine so the slider works in Tauri
  // builds where the HTML <audio> path is bypassed by audio_play_url.
  // Fire-and-forget — the engine clamps + persists nothing, so a
  // dropped call just means the next slider tick re-syncs.
  $: if (isTauri()) {
    void audioSetVolume(muted ? 0 : volume);
  }

  function persistVolume() {
    try {
      localStorage.setItem('onscreen_audio_volume', String(volume));
      localStorage.setItem('onscreen_audio_muted', muted ? '1' : '0');
    } catch { /* private mode */ }
  }

  function onTimeUpdate() {
    if (scrubbing) return;
    const el = activeEl();
    if (!el) return;
    positionMS = Math.round(el.currentTime * 1000);
    audio.setPosition(positionMS);
    if (sleepAtMS !== null && $sleep.mode === 'chapter' && positionMS >= sleepAtMS) {
      sleepAtChapterEnd(sleepAtMS);
      return;
    }
    // Periodic playing-state scrobble so resume position survives reload.
    if (playing && track && (track.id !== lastReportedID || Math.abs(positionMS - lastReportedMS) >= 10000)) {
      void report('playing');
    }
  }

  function onLoadedMeta() {
    const el = activeEl();
    if (!el) return;
    if (Number.isFinite(el.duration) && el.duration > 0) {
      durationMS = Math.round(el.duration * 1000);
    }
    // A load resets playbackRate to defaultPlaybackRate, which applyRate
    // keeps equal to the book's speed; re-assert it in case an engine
    // didn't.
    if (el.playbackRate !== rate) el.playbackRate = rate;
    // Apply any deferred resume seek now that metadata is in. The
    // load reactive block sets el.currentTime optimistically, but
    // browsers can clamp pre-metadata seeks to 0; re-applying here
    // is the safety net.
    if (pendingSeekMS !== null && pendingSeekMS > 0) {
      const target = pendingSeekMS;
      pendingSeekMS = null;
      if (Number.isFinite(el.duration) && target / 1000 < el.duration - 1) {
        el.currentTime = target / 1000;
        positionMS = target;
        audio.setPosition(target);
      }
    }
  }

  function onEnded() {
    // Mark the finished track as stopped at full duration so it doesn't linger
    // as "in progress" — then advance. endedTrackID suppresses the duplicate
    // stop the track-change subscriber would otherwise emit on advance.
    if (track) {
      const d = durationMS || (track.durationMS ?? 0);
      endedTrackID = track.id;
      void itemApi.progress(track.id, d, d, 'stopped').catch(() => {});
    }
    advanceAfterEnd();
  }

  async function onAudioError() {
    // The element can't say why it failed. A stream refused after an admin
    // stop (403 PLAYBACK_STOPPED on the range request) must end playback
    // with the admin's message — skipping to the next track would just keep
    // the music going — so ask the server before treating it as unplayable.
    const t = track;
    const src = loadedSrc;
    const stopped = t && src ? await probePlaybackStopped(src) : null;
    if (track !== t) return; // the listener moved on while we asked
    if (stopped !== null) {
      handleAdminStop(stopped);
      return;
    }
    // Skip past unplayable files instead of getting stuck.
    audio.next();
  }

  function startScrub(e: Event) {
    scrubbing = true;
    updateScrub(e);
  }
  function updateScrub(e: Event) {
    const input = e.target as HTMLInputElement;
    scrubMS = parseInt(input.value, 10);
    positionMS = scrubMS;
  }
  function commitScrub() {
    if (Number.isFinite(scrubMS)) seekTo(scrubMS);
    scrubbing = false;
  }

  // Moves playback within the current track: the seek bar, chapter skips
  // in a single-file book, and audio.seek() (a bookmark in the playing
  // chapter, Previous restarting a track) all land here.
  function seekTo(ms: number) {
    if (!track || !Number.isFinite(ms)) return;
    const target = Math.max(0, Math.round(ms));
    if (nativeActive()) {
      // Native engine path. Optimistic store update so the seek bar
      // snaps to the dropped position immediately rather than
      // rubber-banding back to the old position for a frame.
      //
      // Polling is suspended around the IPC because audio_seek
      // tears down the current pipeline before building the new one;
      // a poll tick landing in the in-between (engine.current = None)
      // window would see playing=false and exit the loop. Restart
      // after the seek settles — at that point the new pipeline is
      // already producing samples so the next tick gets a clean
      // playing=true reading.
      audio.setPosition(target);
      positionMS = target;
      stopNativePolling();
      audioSeek(target, getBearerToken(), null)
        .catch((err) => console.warn('native engine seek failed:', err))
        .finally(() => startNativePolling());
    } else {
      const el = activeEl();
      if (!el) return;
      el.currentTime = target / 1000;
      positionMS = target;
      audio.setPosition(target);
    }
    if ($sleep.mode === 'chapter') sleepAtMS = nextChapterBoundary(track.audiobook?.chapters, target);
    void report(playing ? 'playing' : 'paused');
  }

  // Previous / Next. In a single-file book with chapters they move between
  // its chapters (Previous restarts the current one first, as with a
  // track); from the last chapter Next moves on in the queue. Otherwise
  // they walk the queue.
  function skipPrev() {
    const at = prevChapterStart(track?.audiobook?.chapters, positionMS);
    if (at !== null) seekTo(at);
    else audio.prev();
  }

  function skipNext() {
    const at = nextChapterStart(track?.audiobook?.chapters, positionMS);
    if (at !== null) seekTo(at);
    else audio.next();
  }

  // m:ss, or h:mm:ss for a long single-file audiobook.
  function fmt(ms: number): string {
    return formatPosition(ms);
  }

  function toggleMute() {
    muted = !muted;
    persistVolume();
  }

  function onVolumeChange(e: Event) {
    const v = parseFloat((e.target as HTMLInputElement).value);
    volume = v;
    if (volume > 0) muted = false;
    persistVolume();
  }
</script>

<svelte:window
  on:click={(e) => { onRgWindowClick(e); onMenuWindowClick(e); }}
  on:keydown={(e) => { onRgKeydown(e); if (openMenu && e.key === 'Escape') openMenu = ''; }}
/>

<!-- Two audio elements rotated for gapless transitions. The "active"
     one (whichever activeIsA points at) carries the timeupdate /
     ended / error handlers; the "preload" one just buffers the next
     track. Roles flip on track change; we wire handlers to BOTH
     elements so they always fire on whichever is currently active. -->
<audio
  bind:this={audioElA}
  on:timeupdate={() => activeIsA && onTimeUpdate()}
  on:loadedmetadata={() => activeIsA && onLoadedMeta()}
  on:ended={() => activeIsA && onEnded()}
  on:error={() => activeIsA && onAudioError()}
  preload="auto"
></audio>
<audio
  bind:this={audioElB}
  on:timeupdate={() => !activeIsA && onTimeUpdate()}
  on:loadedmetadata={() => !activeIsA && onLoadedMeta()}
  on:ended={() => !activeIsA && onEnded()}
  on:error={() => !activeIsA && onAudioError()}
  preload="auto"
></audio>

{#if track}
  <aside class="player" aria-label="Audio player">
    <div class="left">
      <button class="close-btn" on:click={() => audio.clear()}
              title="Close player" aria-label="Close player">
        <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor">
          <path d="M2.146 2.854a.5.5 0 1 1 .708-.708L8 7.293l5.146-5.147a.5.5 0 0 1 .708.708L8.707 8l5.147 5.146a.5.5 0 0 1-.708.708L8 8.707l-5.146 5.147a.5.5 0 0 1-.708-.708L7.293 8 2.146 2.854z"/>
        </svg>
      </button>
      {#if track.posterPath}
        <img class="art"
             src={assetUrl(`/artwork/${encodeURI(track.posterPath)}?w=120`)}
             alt={track.album ?? ''} />
      {:else}
        <div class="art placeholder">♪</div>
      {/if}
      <div class="meta">
        <div class="title">{track.title}</div>
        <div class="sub">
          {#if currentChapter}
            {currentChapter.title}{#if track.artist || track.album} · {/if}
          {/if}
          <!-- An audiobook's "artist" is its author and "album" its book. -->
          {#if track.artist}
            {#if track.artistId}
              <a href={track.audiobook ? `/authors/${track.artistId}` : `/artists/${track.artistId}`}>{track.artist}</a>
            {:else}{track.artist}{/if}
          {/if}
          {#if track.artist && track.album} · {/if}
          {#if track.album}
            {#if track.albumId}
              <a href={track.audiobook ? `/audiobooks/${track.albumId}` : `/albums/${track.albumId}`}>{track.album}</a>
            {:else}{track.album}{/if}
          {/if}
        </div>
      </div>
    </div>

    <div class="center">
      <div class="transport">
        <button class="t-btn" class:on={shuffle} on:click={() => audio.toggleShuffle()}
                title="Shuffle" aria-label="Shuffle" aria-pressed={shuffle}>
          <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor">
            <path d="M0 3.5A.5.5 0 0 1 .5 3H1c2.202 0 3.827 1.24 4.874 2.418.49.552.865 1.102 1.126 1.532a1.5 1.5 0 0 1-.001 1.65l-.121.193A12.6 12.6 0 0 0 5.874 9.582C4.827 10.76 3.202 12 1 12H.5a.5.5 0 0 1 0-1H1c1.798 0 3.173-1.01 4.126-2.082.473-.532.806-1.06 1.014-1.396a.5.5 0 0 0 0-.514c-.208-.336-.541-.864-1.014-1.396C4.173 4.51 2.798 3.5 1 3.5H.5a.5.5 0 0 1-.5-.5z"/>
            <path d="M13 5.466V4.5a.25.25 0 0 1 .41-.192l2.36 1.966c.12.1.12.284 0 .384l-2.36 1.966A.25.25 0 0 1 13 8.434V7.5c-1.473 0-2.42 1.05-3.084 2.05L9.36 9.7l.39-.6.234-.413c.65-1.124 1.652-2.221 3.016-2.221zM13 10.466V11.5a.25.25 0 0 0 .41.192l2.36-1.966a.25.25 0 0 0 0-.384l-2.36-1.966A.25.25 0 0 0 13 7.566v.934c-1.473 0-2.42-1.05-3.084-2.05l-.234-.413a14.6 14.6 0 0 1-.39-.6l-.391.625C8.231 6.954 7.225 7.5 5.5 7.5H5a.5.5 0 0 0 0 1h.5c1.725 0 2.731.546 3.401 1.438.144.193.273.39.391.575l.39.6.234.413C10.58 11.95 11.527 13 13 13z"/>
          </svg>
        </button>
        <button class="t-btn" on:click={skipPrev} title="Previous" aria-label="Previous">
          <svg viewBox="0 0 16 16" width="16" height="16" fill="currentColor">
            <path d="M.5 3.5A.5.5 0 0 1 1 4v3.248l6.267-3.636c.52-.302 1.233.043 1.233.696v2.94l6.267-3.636c.52-.302 1.233.043 1.233.696v7.384c0 .653-.713.998-1.233.696L8.5 8.752v2.94c0 .653-.713.998-1.233.696L1 8.752V12a.5.5 0 0 1-1 0V4a.5.5 0 0 1 .5-.5z"/>
          </svg>
        </button>
        <button class="t-btn play" on:click={() => audio.togglePlay()}
                title={playing ? 'Pause' : 'Play'} aria-label={playing ? 'Pause' : 'Play'}>
          {#if playing}
            <svg viewBox="0 0 16 16" width="18" height="18" fill="currentColor">
              <path d="M5.5 3.5A1.5 1.5 0 0 1 7 5v6a1.5 1.5 0 0 1-3 0V5a1.5 1.5 0 0 1 1.5-1.5zm5 0A1.5 1.5 0 0 1 12 5v6a1.5 1.5 0 0 1-3 0V5a1.5 1.5 0 0 1 1.5-1.5z"/>
            </svg>
          {:else}
            <svg viewBox="0 0 16 16" width="18" height="18" fill="currentColor">
              <path d="m11.596 8.697-6.363 3.692c-.54.313-1.233-.066-1.233-.697V4.308c0-.63.692-1.01 1.233-.696l6.363 3.692a.802.802 0 0 1 0 1.393z"/>
            </svg>
          {/if}
        </button>
        <button class="t-btn" on:click={skipNext} title="Next" aria-label="Next">
          <svg viewBox="0 0 16 16" width="16" height="16" fill="currentColor">
            <path d="M15.5 3.5A.5.5 0 0 0 15 4v3.248L8.733 3.612C8.213 3.31 7.5 3.655 7.5 4.308v2.94L1.233 3.612C.713 3.31 0 3.655 0 4.308v7.384c0 .653.713.998 1.233.696L7.5 8.752v2.94c0 .653.713.998 1.233.696L15 8.752V12a.5.5 0 0 0 1 0V4a.5.5 0 0 0-.5-.5z"/>
          </svg>
        </button>
        <button class="t-btn" class:on={repeat !== 'off'} on:click={() => audio.cycleRepeat()}
                title="Repeat: {repeat}" aria-label="Repeat: {repeat}">
          {#if repeat === 'one'}
            <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor">
              <path d="M11 5.466V4H5a4 4 0 0 0-3.584 5.777.5.5 0 1 1-.896.446A5 5 0 0 1 5 3h6V1.534a.25.25 0 0 1 .41-.192l2.36 1.966c.12.1.12.284 0 .384l-2.36 1.966a.25.25 0 0 1-.41-.192zm3.81.086a.5.5 0 0 1 .67.225A5 5 0 0 1 11 13H5v1.466a.25.25 0 0 1-.41.192l-2.36-1.966a.25.25 0 0 1 0-.384l2.36-1.966a.25.25 0 0 1 .41.192V11h6a4 4 0 0 0 3.585-5.777.5.5 0 0 1 .225-.67z"/>
              <path d="M9 5.5a.5.5 0 0 0-.854-.354l-1.5 1.5a.5.5 0 1 0 .708.708L8 6.707V10.5a.5.5 0 0 0 1 0z"/>
            </svg>
          {:else}
            <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor">
              <path d="M11 5.466V4H5a4 4 0 0 0-3.584 5.777.5.5 0 1 1-.896.446A5 5 0 0 1 5 3h6V1.534a.25.25 0 0 1 .41-.192l2.36 1.966c.12.1.12.284 0 .384l-2.36 1.966a.25.25 0 0 1-.41-.192zm3.81.086a.5.5 0 0 1 .67.225A5 5 0 0 1 11 13H5v1.466a.25.25 0 0 1-.41.192l-2.36-1.966a.25.25 0 0 1 0-.384l2.36-1.966a.25.25 0 0 1 .41.192V11h6a4 4 0 0 0 3.585-5.777.5.5 0 0 1 .225-.67z"/>
            </svg>
          {/if}
        </button>
      </div>
      <div class="seek-row">
        <span class="t">{fmt(positionMS)}</span>
        <input
          type="range"
          class="seek"
          min="0"
          max={Math.max(durationMS, 1)}
          step="1000"
          value={positionMS}
          on:input={updateScrub}
          on:mousedown={startScrub}
          on:touchstart={startScrub}
          on:change={commitScrub}
          aria-label="Seek"
        />
        <span class="t">{fmt(durationMS)}</span>
      </div>
    </div>

    <div class="right">
      {#if track.audiobook}
        <!-- Listening speed, saved per book. -->
        <div class="ctl-wrap" bind:this={speedWrapEl}>
          <button
            class="ctl-btn rate-btn"
            class:on={rate !== 1}
            on:click={() => toggleMenu('speed')}
            title="Playback speed"
            aria-label="Playback speed {formatRate(rate)}"
            aria-haspopup="dialog"
            aria-expanded={openMenu === 'speed'}
          >{formatRate(rate)}</button>
          {#if openMenu === 'speed'}
            <div class="ctl-menu" role="dialog" aria-label="Playback speed">
              <div class="ctl-head">Playback speed</div>
              <div class="ctl-grid" role="radiogroup" aria-label="Playback speed">
                {#each RATE_PRESETS as r (r)}
                  <button
                    class="ctl-opt"
                    class:active={rate === r}
                    role="radio"
                    aria-checked={rate === r}
                    on:click={() => chooseRate(r)}
                  >{formatRate(r)}</button>
                {/each}
              </div>
            </div>
          {/if}
        </div>
        {#if listeningApi}
          <div class="ctl-wrap" bind:this={bookmarkWrapEl}>
            <button
              class="ctl-btn"
              class:on={openMenu === 'bookmark'}
              on:click={openBookmark}
              title="Add bookmark"
              aria-label="Add bookmark"
              aria-haspopup="dialog"
              aria-expanded={openMenu === 'bookmark'}
            >
              <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor"><path d="M2 2a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v13.5a.5.5 0 0 1-.777.416L8 13.101l-5.223 2.815A.5.5 0 0 1 2 15.5V2zm2-1a1 1 0 0 0-1 1v12.566l4.723-2.482a.5.5 0 0 1 .554 0L13 14.566V2a1 1 0 0 0-1-1H4z"/><path d="M8 4a.5.5 0 0 1 .5.5V6H10a.5.5 0 0 1 0 1H8.5v1.5a.5.5 0 0 1-1 0V7H6a.5.5 0 0 1 0-1h1.5V4.5A.5.5 0 0 1 8 4z"/></svg>
            </button>
            {#if openMenu === 'bookmark'}
              <form class="ctl-menu" aria-label="Add bookmark" on:submit|preventDefault={saveBookmark}>
                <div class="ctl-head">Bookmark at {formatPosition(bookmarkMS)}</div>
                <input
                  class="ctl-input"
                  type="text"
                  maxlength="500"
                  placeholder="Note (optional)"
                  aria-label="Bookmark note"
                  bind:value={bookmarkNote}
                  use:focusOnMount
                />
                <div class="ctl-actions">
                  <button type="button" class="ctl-cancel" on:click={() => (openMenu = '')}>Cancel</button>
                  <button type="submit" class="ctl-save" disabled={bookmarkSaving}>Save</button>
                </div>
              </form>
            {/if}
          </div>
        {/if}
      {/if}
      <div class="ctl-wrap" bind:this={sleepWrapEl}>
        <button
          class="ctl-btn"
          class:on={$sleep.mode !== 'off'}
          on:click={() => toggleMenu('sleep')}
          title={$sleep.mode === 'off' ? 'Sleep timer'
            : $sleep.mode === 'chapter' ? `Sleep timer: end of ${sleepUnit}`
            : `Sleep timer: ${formatRemaining($sleep.remainingMs)} left`}
          aria-label="Sleep timer"
          aria-haspopup="dialog"
          aria-expanded={openMenu === 'sleep'}
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="14" height="14"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
          {#if $sleep.mode !== 'off' && sleepLeftMS !== null}
            <span class="ctl-left">{formatRemaining(sleepLeftMS)}</span>
          {/if}
        </button>
        {#if openMenu === 'sleep'}
          <div class="ctl-menu" role="dialog" aria-label="Sleep timer">
            <div class="ctl-head">Sleep timer</div>
            <div class="ctl-list" role="radiogroup" aria-label="Sleep timer">
              <button
                class="ctl-opt"
                class:active={$sleep.mode === 'off'}
                role="radio"
                aria-checked={$sleep.mode === 'off'}
                on:click={() => pickSleep('off')}
              >Off</button>
              {#each SLEEP_OPTIONS as o (o.mode)}
                <button
                  class="ctl-opt"
                  class:active={$sleep.mode === o.mode}
                  role="radio"
                  aria-checked={$sleep.mode === o.mode}
                  on:click={() => pickSleep(o.mode)}
                >{o.label}</button>
              {/each}
              <button
                class="ctl-opt"
                class:active={$sleep.mode === 'chapter'}
                role="radio"
                aria-checked={$sleep.mode === 'chapter'}
                on:click={() => pickSleep('chapter')}
              >End of {sleepUnit}</button>
            </div>
          </div>
        {/if}
      </div>
      {#if rgRouter}
        <!-- Browser ReplayGain. Desktop builds configure the native
             engine's ReplayGain on /native/audio instead. -->
        <div class="rg-wrap" bind:this={rgWrapEl}>
          <button
            class="rg-btn"
            class:on={rgMode !== 'off'}
            on:click={() => (rgMenuOpen = !rgMenuOpen)}
            title={rgMode === 'off' ? 'ReplayGain: off' : `ReplayGain: ${rgMode} mode`}
            aria-label="ReplayGain settings"
            aria-haspopup="dialog"
            aria-expanded={rgMenuOpen}
          >RG{#if rgMode !== 'off'}<span class="rg-btn-mode">{rgMode === 'track' ? 'T' : 'A'}</span>{/if}</button>
          {#if rgMenuOpen}
            <div class="rg-menu" role="dialog" aria-label="ReplayGain">
              <div class="rg-head">ReplayGain</div>
              <div class="rg-modes" role="radiogroup" aria-label="ReplayGain mode">
                {#each RG_MODES as m (m.value)}
                  <button
                    class="rg-mode"
                    class:active={rgMode === m.value}
                    role="radio"
                    aria-checked={rgMode === m.value}
                    on:click={() => chooseRgMode(m.value)}
                  >{m.label}</button>
                {/each}
              </div>
              <label class="rg-preamp">
                <span>Preamp</span>
                <input
                  type="range"
                  min={PREAMP_MIN_DB}
                  max={PREAMP_MAX_DB}
                  step="0.5"
                  value={rgPreampDb}
                  disabled={rgMode === 'off'}
                  on:input={onRgPreampInput}
                  aria-label="ReplayGain preamp"
                />
                <span class="rg-db">{fmtDb(rgPreampDb)}</span>
              </label>
              {#if rgDisabledReason}
                <p class="rg-note warn">Unavailable in this browser: {rgDisabledReason}.</p>
              {:else if rgMode !== 'off' && rgCurrentTags !== undefined}
                {#if rgCurrentSel}
                  <p class="rg-note">
                    This track: {fmtDb(rgAppliedDb, 2)}{#if rgMode === 'album' && rgCurrentTags.albumGain === undefined}
                      (no album tag, using track){/if}
                  </p>
                {:else}
                  <p class="rg-note">This track has no ReplayGain tags — playing at its native level.</p>
                {/if}
              {/if}
              <a class="rg-link" href="/native/audio" on:click={() => (rgMenuOpen = false)}>Audio settings</a>
            </div>
          {/if}
        </div>
      {/if}
      <button class="vol-btn" on:click={toggleMute} aria-label={muted ? 'Unmute' : 'Mute'}>
        {#if muted || volume === 0}
          <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor"><path d="M6.717 3.55A.5.5 0 0 1 7 4v8a.5.5 0 0 1-.812.39L3.825 10.5H1.5A.5.5 0 0 1 1 10V6a.5.5 0 0 1 .5-.5h2.325l2.363-1.89a.5.5 0 0 1 .529-.06zm5.927.346a.5.5 0 0 1 .708 0L15 5.293l1.646-1.647a.5.5 0 0 1 .708.708L15.707 6l1.647 1.646a.5.5 0 0 1-.707.708L15 6.707l-1.646 1.647a.5.5 0 0 1-.708-.708L14.293 6l-1.647-1.646a.5.5 0 0 1 0-.708z"/></svg>
        {:else}
          <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor"><path d="M11.536 14.01A8.473 8.473 0 0 0 14.026 8a8.473 8.473 0 0 0-2.49-6.01l-.708.707A7.476 7.476 0 0 1 13.025 8c0 2.071-.84 3.946-2.197 5.303l.708.707z"/><path d="M10.121 12.596A6.48 6.48 0 0 0 12.025 8a6.48 6.48 0 0 0-1.904-4.596l-.707.707A5.483 5.483 0 0 1 11.025 8a5.483 5.483 0 0 1-1.61 3.89l.706.706z"/><path d="M8.707 11.182A4.486 4.486 0 0 0 10.025 8a4.486 4.486 0 0 0-1.318-3.182L8 5.525A3.489 3.489 0 0 1 9.025 8 3.49 3.49 0 0 1 8 10.475l.707.707z"/><path d="M6.717 3.55A.5.5 0 0 1 7 4v8a.5.5 0 0 1-.812.39L3.825 10.5H1.5A.5.5 0 0 1 1 10V6a.5.5 0 0 1 .5-.5h2.325l2.363-1.89a.5.5 0 0 1 .529-.06z"/></svg>
        {/if}
      </button>
      <input
        type="range"
        class="vol"
        min="0"
        max="1"
        step="0.01"
        value={muted ? 0 : volume}
        on:input={onVolumeChange}
        aria-label="Volume"
      />
    </div>
  </aside>
{/if}

<style>
  .player {
    position: fixed;
    left: 216px; /* sidebar width */
    right: 0;
    bottom: 0;
    height: 76px;
    background: var(--bg-secondary);
    border-top: 1px solid var(--border);
    display: grid;
    grid-template-columns: minmax(220px, 1fr) minmax(280px, 2fr) minmax(180px, 1fr);
    align-items: center;
    padding: 0 1.25rem;
    gap: 1rem;
    z-index: 50;
    box-shadow: 0 -4px 20px rgba(0,0,0,0.25);
  }

  .left { display: flex; align-items: center; gap: 0.75rem; min-width: 0; }
  .art {
    width: 52px; height: 52px; border-radius: 4px; object-fit: cover;
    background: var(--bg-elevated); flex-shrink: 0;
  }
  .art.placeholder {
    display: flex; align-items: center; justify-content: center;
    color: var(--text-muted); font-size: 1.5rem;
  }
  .meta { min-width: 0; }
  .title { font-size: 0.85rem; font-weight: 500; overflow: hidden;
           text-overflow: ellipsis; white-space: nowrap; }
  .sub { font-size: 0.72rem; color: var(--text-muted); overflow: hidden;
         text-overflow: ellipsis; white-space: nowrap; }
  .sub a { color: inherit; text-decoration: none; }
  .sub a:hover { color: var(--text-secondary); }

  .center { display: flex; flex-direction: column; align-items: center; gap: 0.3rem; min-width: 0; }
  .transport { display: flex; align-items: center; gap: 0.6rem; }
  .t-btn {
    background: none; border: 0; cursor: pointer;
    padding: 0.4rem; border-radius: 4px;
    color: var(--text-muted); display: inline-flex;
    transition: color 0.12s;
  }
  .t-btn:hover { color: var(--text-primary); }
  .t-btn.on { color: var(--accent); }
  .t-btn.play {
    background: var(--text-primary); color: var(--bg-primary);
    border-radius: 999px; padding: 0.5rem;
  }
  .t-btn.play:hover { background: var(--accent); color: white; }

  .seek-row { display: flex; align-items: center; gap: 0.6rem; width: 100%; max-width: 540px; }
  .t { font-size: 0.7rem; color: var(--text-muted); font-variant-numeric: tabular-nums; min-width: 2.6rem; text-align: center; }
  .seek {
    flex: 1; -webkit-appearance: none; appearance: none; height: 4px;
    background: var(--border-strong); border-radius: 2px; cursor: pointer; outline: none;
  }
  .seek::-webkit-slider-thumb {
    -webkit-appearance: none; appearance: none; width: 12px; height: 12px;
    border-radius: 50%; background: var(--text-primary); cursor: pointer;
    border: 0;
  }
  .seek::-moz-range-thumb {
    width: 12px; height: 12px; border-radius: 50%;
    background: var(--text-primary); cursor: pointer; border: 0;
  }

  .right { display: flex; align-items: center; gap: 0.5rem; justify-content: flex-end; }
  .vol-btn {
    background: none; border: 0; cursor: pointer; color: var(--text-muted);
    padding: 0.3rem; border-radius: 4px;
  }
  .vol-btn:hover { color: var(--text-primary); }
  .vol {
    width: 90px; -webkit-appearance: none; appearance: none; height: 4px;
    background: var(--border-strong); border-radius: 2px; cursor: pointer; outline: none;
  }
  .vol::-webkit-slider-thumb {
    -webkit-appearance: none; appearance: none; width: 10px; height: 10px;
    border-radius: 50%; background: var(--text-primary); cursor: pointer; border: 0;
  }
  .vol::-moz-range-thumb {
    width: 10px; height: 10px; border-radius: 50%;
    background: var(--text-primary); cursor: pointer; border: 0;
  }

  .rg-wrap { position: relative; display: inline-flex; }
  .rg-btn {
    background: none; border: 1px solid var(--border-strong); cursor: pointer;
    color: var(--text-muted); border-radius: 4px; padding: 0.1rem 0.35rem;
    font-size: 0.62rem; font-weight: 700; letter-spacing: 0.04em; line-height: 1.4;
  }
  .rg-btn:hover { color: var(--text-primary); }
  .rg-btn.on { color: var(--accent); border-color: var(--accent); }
  .rg-btn-mode { margin-left: 0.2rem; }
  .rg-menu, .ctl-menu {
    position: absolute; bottom: calc(100% + 10px); right: 0; z-index: 60;
    width: 240px; padding: 0.75rem;
    background: var(--bg-elevated, var(--bg-secondary)); border: 1px solid var(--border);
    border-radius: 8px; box-shadow: 0 6px 24px rgba(0,0,0,0.35);
    display: flex; flex-direction: column; gap: 0.6rem;
  }
  .ctl-menu { width: 220px; }
  .rg-head, .ctl-head { font-size: 0.78rem; font-weight: 600; color: var(--text-primary); }
  .rg-modes { display: grid; grid-template-columns: repeat(3, 1fr); gap: 0.3rem; }
  .rg-mode, .ctl-opt {
    background: none; border: 1px solid var(--border); border-radius: 5px; cursor: pointer;
    color: var(--text-secondary); font-size: 0.72rem; padding: 0.3rem 0;
  }
  .rg-mode:hover, .ctl-opt:hover { border-color: var(--accent); }
  .rg-mode.active, .ctl-opt.active { border-color: var(--accent); color: var(--accent); font-weight: 600; }

  /* Listening controls: speed, bookmark, sleep timer. */
  .ctl-wrap { position: relative; display: inline-flex; }
  .ctl-btn {
    background: none; border: 0; cursor: pointer; color: var(--text-muted);
    padding: 0.3rem; border-radius: 4px;
    display: inline-flex; align-items: center; gap: 0.25rem;
  }
  .ctl-btn:hover { color: var(--text-primary); }
  .ctl-btn.on { color: var(--accent); }
  .rate-btn {
    border: 1px solid var(--border-strong); padding: 0.1rem 0.35rem;
    font-size: 0.68rem; font-weight: 700; line-height: 1.4; font-variant-numeric: tabular-nums;
  }
  .rate-btn.on { border-color: var(--accent); }
  .ctl-left { font-size: 0.68rem; font-variant-numeric: tabular-nums; }
  .ctl-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 0.3rem; }
  .ctl-list { display: flex; flex-direction: column; gap: 0.3rem; }
  .ctl-list .ctl-opt { text-align: left; padding: 0.35rem 0.6rem; }
  .ctl-input {
    width: 100%; box-sizing: border-box; padding: 0.4rem 0.5rem; font-size: 0.78rem;
    background: var(--input-bg, var(--bg-primary)); color: var(--text-primary);
    border: 1px solid var(--border-strong); border-radius: 5px;
  }
  .ctl-input:focus { outline: none; border-color: var(--accent); }
  .ctl-actions { display: flex; justify-content: flex-end; gap: 0.4rem; }
  .ctl-cancel, .ctl-save {
    border-radius: 5px; cursor: pointer; font-size: 0.72rem; padding: 0.3rem 0.7rem;
  }
  .ctl-cancel { background: none; border: 1px solid var(--border); color: var(--text-secondary); }
  .ctl-cancel:hover { color: var(--text-primary); }
  .ctl-save { background: var(--accent); border: 0; color: white; font-weight: 600; }
  .ctl-save:disabled { opacity: 0.6; cursor: default; }
  .rg-preamp { display: flex; align-items: center; gap: 0.5rem; font-size: 0.7rem; color: var(--text-muted); }
  .rg-preamp input { flex: 1; min-width: 0; }
  .rg-db { min-width: 3.6rem; text-align: right; font-variant-numeric: tabular-nums; }
  .rg-note { margin: 0; font-size: 0.7rem; color: var(--text-muted); line-height: 1.35; }
  .rg-note.warn { color: var(--warning, #f59e0b); }
  .rg-link { font-size: 0.7rem; color: var(--text-secondary); text-decoration: none; align-self: flex-start; }
  .rg-link:hover { color: var(--text-primary); text-decoration: underline; }

  .close-btn {
    background: none; border: 0; cursor: pointer;
    color: var(--text-muted); padding: 0.3rem; border-radius: 4px;
    display: inline-flex; align-items: center; justify-content: center;
  }
  .close-btn:hover { color: var(--text-primary); }

  /* Mobile: stack and shorten — sit above the bottom nav (60px). */
  @media (max-width: 768px) {
    .player {
      left: 0;
      bottom: 60px;
      height: auto;
      padding: 0.5rem 0.75rem;
      grid-template-columns: 1fr auto;
      grid-template-areas:
        "left right"
        "center center";
      gap: 0.5rem;
    }
    .left { grid-area: left; gap: 0.5rem; }
    .right { grid-area: right; gap: 0.25rem; }
    .center { grid-area: center; gap: 0.2rem; }
    .art { width: 40px; height: 40px; }
    .vol { display: none; }
    .seek-row { max-width: none; }
  }
</style>
