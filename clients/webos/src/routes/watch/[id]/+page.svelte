<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import {
    api,
    endpoints,
    ApiError,
    Unauthorized,
    supportsHEVC,
    supportsAV1,
    demoteCodec,
    type ChildItem,
    type ItemDetail,
    type Chapter,
    type Marker,
    type NotificationEvent,
    type TranscodeSession
  } from '$lib/api';
  import type { UserPreferences } from '$lib/api/endpoints';
  import { focusManager } from '$lib/focus/manager';
  import type { RemoteKey } from '$lib/focus/keys';
  import Spinner from '$lib/components/Spinner.svelte';
  import { loadHls } from '$lib/player/hls-loader';
  import type HlsType from 'hls.js';
  import {
    CONTENT_REVOKED_TEXT,
    ProgressReporter,
    settleWithin,
    type HeartbeatRefusal,
    type HeartbeatState
  } from '$lib/player/progress-reporter';
  import { parseVtt, findCue, type TrickplayCue } from '$lib/player/trickplay';
  import { findSiblings, resolveNext } from '$lib/player/siblings';
  import {
    contentDurationMs,
    firstLoadPositionSec,
    hlsSessionConfig,
    hlsStartPositionSec,
    inStreamStartMs,
    isStarved,
    planContentSeek,
    playlistWindow,
    REISSUE_END_GUARD_MS,
    seg0GapMs,
    sessionOffsetMs,
    syncSeekStreamSec,
    toContentMs,
    validAudioOrdinal,
    type StreamWindow
  } from '$lib/player/session';
  import { armFirstLoad } from '$lib/player/first-load';
  import { PendingScrub } from '$lib/player/scrub';
  import {
    PlayIntent,
    SessionController,
    type OpenOutcome,
    type OpenRequest
  } from '$lib/player/session-controller';
  import {
    applyPlan,
    freshRecovery,
    planFatal,
    type FatalKind,
    type RecoveryState
  } from '$lib/player/recovery';
  import type { OnlineSubtitle } from '$lib/api';
  import { pickPreferredAudio, pickPreferredSubtitle } from '$lib/subtitleSelect';
  import { audioTrackLabel } from '$lib/langName';
  import {
    SUBTITLE_FETCH_TIMEOUT_MS,
    activeCues,
    buildSubtitleOptions,
    loadSubtitleCues,
    pickDownloadedSubtitle,
    ptsShiftOnFirstPlayMs,
    sameCues,
    subtitleClockMs,
    subtitleFetchError,
    type Cue
  } from '$lib/player/subtitles';
  import {
    ACTION_LABELS,
    TRANSPORT_SKIP_MS,
    actionRowKey,
    barSeekMs,
    chapterAt,
    chapterRowLabel,
    pickerWindow,
    playerActions,
    subtitlePickerRows,
    transportButtons,
    transportSteps,
    transportLabel,
    type PickerRow,
    type PlayerAction,
    type TransportButton
  } from '$lib/player/actions';
  import { POINTER_ECHO_MS, pointerShown } from '$lib/focus/pointer';
  import { ensureNavGates, onlineSubtitlesGate } from '$lib/navGates';
  import {
    clearsSpinnerOnFragBuffered,
    directAudioFailure,
    endedAction,
    navigatesAfterWait,
    probeOutcome,
    PlayheadWatch
  } from '$lib/player/gates';
  import {
    channelStep,
    controlsHideDelayMs,
    episodeContextLine,
    nowPlayingArt,
    queueLabel,
    queueNoun,
    upNextKey,
    usesParentCover,
    type UpNextButton
  } from '$lib/player/chrome';
  import { PROGRESS_UPDATED_EVENT, events } from '$lib/events';
  import { goBack, replaceTo, takeStartOverride } from '$lib/nav';
  import { clientName } from '$lib/device';
  import {
    PLAYBACK_STOP_EVENT,
    adminStopText,
    isPlaybackStoppedError,
    isStopForPlayer,
    parsePlaybackStop,
    probePlaybackStopped,
    stoppedTextFromServer
  } from '$lib/playbackStop';
  import {
    RATE_PRESETS,
    RateCheck,
    applyMediaRate,
    clampRate,
    formatRate,
    hasListeningSpeed,
    presetIndex,
    sameRate
  } from '$lib/audiobookSpeed';

  const itemID = page.params.id!;
  // "Watch again" / the show's up-next episode asked for a start point (see
  // playItem in nav.ts). Read once: a retry of a failed load mustn't lose it.
  const startOverride = takeStartOverride(itemID);
  let video: HTMLVideoElement | undefined = $state();

  let item = $state<ItemDetail | null>(null);
  // The message on the error overlay ('' = none). Every failure ends up here
  // (or in `refused` below): nothing may leave "Starting playback…" up.
  let error = $state('');
  // Whether OK / Play on the error overlay retries from where it stopped
  // (Android re-prepares on Play after an error). Not for refusals, and not
  // for what a retry can't change (a book, Dolby Vision, no file).
  let errorRetryable = $state(false);
  // Where and how a retry re-opens: the position the failure left, and the
  // settings of the stream that failed (a failed audio switch retries the
  // new track, a failed fallback retries the transcode).
  let retryReq: OpenRequest = { positionMs: 0, videoCopy: false, audioOrdinal: null, autoplay: true };
  let loading = $state(true);
  // Rebuffering mid-play (waiting / seeking / a session re-issue): a spinner
  // over the picture. "Starting playback…" covers the first start.
  let buffering = $state(false);
  let paused = $state(true);
  let position = $state(0);
  let duration = $state(0);
  let controlsVisible = $state(true);
  let controlsTimer: ReturnType<typeof setTimeout> | null = null;

  let hls: InstanceType<typeof HlsType> | null = null;
  // The playlist currently loaded into hls and the startPosition it opened
  // at — kept so the error handler can re-init the same session as a last
  // resort on an unrecoverable fatal.
  let currentPlaylistUrl = '';
  let currentStartSec = -1;
  // The live session's first audible frame in stream seconds (its measured
  // seg0 audio gap; 0 = none): an early first load never starts before it
  // (see armFirstLoad).
  let currentMinStartSec = 0;
  let session: TranscodeSession | null = null;
  let reporter: ProgressReporter | null = null;
  // Whether the user means it to play. The element's own paused flag can't
  // say: hls.destroy() and a media detach call load(), which pauses the
  // element without a 'pause' event, so between a source swap and the new
  // source's loadedmetadata video.paused read true for a player that was
  // playing (a Home press or an audio switch there resumed it paused). Set by
  // the play / pause keys and the element's own play / pause on a ready
  // stream; every open stamps it (intent.stamp()) and the attach keeps a
  // newer word over the request's (see PlayIntent): a pause pressed while a
  // re-issue or "Starting playback…" was out used to be undone on attach.
  const intent = new PlayIntent(true);
  // Leaving (Back, the end, the next item): the final report is out, so no
  // key or event does anything more.
  let leaving = false;

  // ── Stream timeline ────────────────────────────────────────────────
  //
  // `position` and `duration` are CONTENT time (a position in the file)
  // everywhere: the bar, the progress heartbeat, markers, chapters, the
  // trickplay lookup, Up Next and cross-device sync. A server session opened
  // at a resume point starts its own clock at 0, so on one
  //   position = video.currentTime * 1000 + offsetMs
  // (see $lib/player/session.ts). Reading the raw currentTime as the
  // position (and seeking the stream to the resume point on top of the
  // session's own offset) showed a film resumed at 45:00 near 0:00, and the
  // heartbeat saved that over the user's real resume point.
  //
  // True while the player reads a server HLS session; false for direct audio.
  let serverStream = false;
  // Content time the live session's stream opens at (0 for direct audio).
  let offsetMs = 0;
  // Whether the live session stream-copies the video (a remux). Every
  // re-issue keeps it: dropping it turned an audio switch or an app resume on
  // a remux into a full re-encode.
  let sessionVideoCopy = false;
  // Played from the file's direct stream (music, audiobooks and audio-only
  // podcast episodes) rather than a server session. Wider than isAudioItem,
  // which is by type: a podcast's audio episode has no video codec but isn't
  // an audio type, and suspend / resume must re-bind its direct source
  // instead of re-issuing it as an HLS transcode.
  let isAudioOnly = $state(false);
  // A stream (a session or the direct source) is bound to the element and
  // not torn down since: the player has a position of its own.
  let bound = false;
  // Bumped by every attach: work started for one stream (a dead-stream
  // probe) can tell whether that stream is still the one bound.
  let attachGen = 0;
  // False from the moment a source is attached until its loadedmetadata.
  // Until then the element's clock is meaningless (it resets to 0 on a new
  // source before hls.js seeks to the start position), so timeupdate
  // ignores it and seeks wait.
  let streamReady = false;
  // What onEnded needs to tell the end of the item from a stream that only
  // stopped (endedAction, $lib/player/gates): an 'ended' on a track that
  // never got going reported it watched at its full length and skipped it.
  // The element's own clock can't say where it was (Chromium reports
  // currentTime = duration once a stream has ended, early or not), so
  // `playhead` follows every start, seek and timeupdate of the bound source
  // (PlayheadWatch). falseEndRetryAtMs: where this page's last early-end
  // retry re-opened.
  const playhead = new PlayheadWatch();
  let falseEndRetryAtMs: number | null = null;
  // One-shot seek for the paths hls.js's startPosition doesn't cover:
  // direct audio's resume point and the native-HLS fallback's start.
  let metadataSeekSec: number | null = null;
  // The page was torn down; a start still in flight retires its session.
  let destroyed = false;
  // The start point is known (the item is loaded): a key seek before that
  // would count from 0.
  let positionKnown = false;
  // The load got as far as asking for a stream; until then a retry runs
  // the whole load again.
  let setupDone = false;

  // ←/→/◀◀/▶▶ build one pending scrub target (shown on the bar and in the
  // trickplay preview) that commits after a short idle or on OK; Back drops
  // it. See $lib/player/scrub.ts. A target from the keys is relative: a
  // little past a fresh session's edge it clamps there instead of re-issuing.
  let scrubTargetMs = $state<number | null>(null);
  const scrub = new PendingScrub({
    commit: (t, relative) => seekToContent(t, relative),
    ready: () => streamReady && !controller.opening,
    changed: (t) => (scrubTargetMs = t),
  });

  // The server ended playback: an admin "stop this stream" (the
  // playback.stop event or a 403 PLAYBACK_STOPPED), a parental watch limit,
  // or the title leaving this profile's reach mid-session (any other 403 on
  // a heartbeat). The player is torn down, the message stays on screen and
  // only Back does anything.
  let refused = $state(false);

  // Audiobook listening speed (books + their chapter files; music stays at
  // 1×) through <video>.playbackRate. RateCheck withdraws the control with a
  // note if the TV accepts a rate but keeps playing at 1×.
  let speed = $state(1);
  let speedPickerOpen = $state(false);
  let speedCursor = $state(1);
  let speedUnsupported = $state(false);
  const rateCheck = new RateCheck();
  let rateCheckTimer: ReturnType<typeof setInterval> | null = null;

  // Chapters: surface as jump targets. Start offsets used for green-button cycling.
  const chapters = $derived<Chapter[]>(item?.files[0]?.chapters ?? []);

  // Audio, subtitle and chapter pickers. They open from the on-screen
  // action row (Down from the controls, then OK; the pointer clicks it too)
  // or the colour keys: yellow audio, blue subtitles. Picking an audio track
  // on an HLS session re-issues the transcode with a new audio_stream_index;
  // subtitles are fetched as WebVTT and drawn by the page (see Subtitles).
  const audioStreams = $derived(item?.files[0]?.audio_streams ?? []);
  let audioPickerOpen = $state(false);
  let subtitlePickerOpen = $state(false);
  let chapterPickerOpen = $state(false);
  let pickerCursor = $state(0);
  // The active audio track as its 0-based ORDINAL within audio_streams —
  // exactly what the server's audio_stream_index takes (`-map 0:a:N`), and
  // the picker's row number. Never AudioStream.index: that is the absolute
  // ffprobe index, which also counts the video and subtitle streams and got
  // the wrong track or a 400 "out of range". 0 is the server's default track.
  // attachOpened records it once a session with it is live; every re-issue
  // (seek, demotion, fallback, resume, retry) sends it again.
  let activeAudioIndex = $state(0);
  // The track preferred_audio_lang resolves to (pickPreferredAudio), sent
  // with the FIRST start only; null = no match, the server's default track.
  // A track picked in this session replaces it for every later re-issue.
  let preferredAudioOrdinal: number | null = null;

  // ── Subtitles ──────────────────────────────────────────────────────
  //
  // Server HLS sessions carry no subtitle renditions, so the element's own
  // text tracks were always empty and subtitles never showed. The chosen
  // track is fetched as WebVTT (embedded: by its absolute stream index;
  // downloaded: from files[].external_subtitles), parsed, and its cues drawn
  // in .subtitle-overlay against the CONTENT clock ($lib/player/subtitles).
  const subtitleOptions = $derived(buildSubtitleOptions(item?.files[0]));
  // The chosen row by key ('emb:<index>' / 'ext:<id>'), null = Off. Keyed,
  // not by position, so it survives every re-issue and an item refresh.
  let activeSubtitleKey = $state<string | null>(null);
  // The user picked a subtitle (or Off) in this session: the saved
  // preference no longer applies (a retry's re-run of the load would
  // otherwise put it back).
  let subtitleChosen = false;
  // The chosen track's cues, and the ones on screen now (raw: compared by
  // identity, so the overlay redraws only when they change).
  let cues: Cue[] = [];
  let shownCues = $state.raw<Cue[]>([]);
  // Fetch generation: a load for a pick the user has since replaced is
  // dropped, never drawn over the newer one (and its request aborted).
  let subtitleGen = 0;
  let subtitleFetch: AbortController | null = null;
  let subtitleLoad = $state<'idle' | 'loading' | 'ready' | 'failed'>('idle');
  // The track whose load last gave up. The choice falls back to Off (the
  // picker's ● must not mark a track that never showed); its row reads
  // "unavailable" until a pick of it loads.
  let subtitleFailedKey = $state<string | null>(null);
  // "Subtitles unavailable" (a load that gave up), shown for 5 s from the
  // moment the picture is up: a load that failed during "Starting
  // playback…" (where the overlay is hidden) used to expire unseen.
  let subtitleNotice = $state('');
  let subtitleNoticeTimer: ReturnType<typeof setTimeout> | null = null;
  // A container timestamp shift taken off the cue clock (see
  // ptsShiftOnFirstPlayMs), measured once per session on its first
  // 'playing': ptsCaptureStartSec is where that session was asked to start,
  // null once measured (or after a seek, which spoils the measurement).
  let ptsShiftMs = 0;
  let ptsCaptureStartSec: number | null = null;

  // ── On-screen actions ──────────────────────────────────────────────
  //
  // The row of buttons under the bar ($lib/player/actions): the focused
  // one's index, -1 when the row doesn't have focus. Kept while a picker it
  // opened is up, so focus returns to the button when the picker closes.
  let actionFocus = $state(-1);
  // When a pointer click last landed: the Magic Remote's OK in pointer mode
  // can also arrive as an Enter keydown, which must not act a second time
  // (the focus manager drops it first: over a button, OK is its click).
  let lastClickAt = 0;
  // The pointer moved after the last key press: only then does hovering a
  // button or a picker row move the focus / cursor to it (a list scrolling
  // under a resting pointer must not drag the d-pad's cursor along).
  let lastPointerMoveAt = 0;
  let lastKeyAt = 0;
  // Focus a hover put on a button (the action row, the Up Next card) holds
  // only while the pointer is on it: leaving puts back the focus from before
  // (see hoverFocus). A pointer crossing [Subtitles] on its way across the
  // screen used to leave the row focused, and the next OK (meant for Up Next
  // or pause) opened the picker. A key press or a click makes it the user's.
  let unhover: (() => void) | null = null;

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
  let prevSibling = $state<ChildItem | null>(null);
  // The parent context (loadParentContext): a track's album and artist, a
  // chapter's book, a podcast episode's podcast (in the album slot), and
  // the parent's cover for an item with no art of its own; the position in
  // the parent's listing. Surfaced in the now-playing UI.
  let albumTitle = $state('');
  let artistTitle = $state('');
  let parentPoster = $state<string | null>(null);
  let queuePosition = $state(0);
  let queueTotal = $state(0);
  // An episode's "Show · S2 · E5", over its title on the controls.
  let episodeContext = $state('');
  let upNextShown = $state(false);
  let upNextCountdown = $state(10);
  let upNextTimer: ReturnType<typeof setInterval> | null = null;
  // The card's focused button (0 Play now, 1 Cancel), -1 when the card
  // doesn't have focus (Down handed the keys back to the player). It takes
  // focus when it shows, as Android's does (see upNextKey).
  let upNextFocus = $state(-1);
  // Back on the Up Next card declines it for the rest of this play: the
  // card doesn't come back, and the end of the episode leaves instead of
  // advancing (Android's Cancel). It used to advance at EOS anyway.
  let upNextDeclined = false;
  // The next-item lookup (it can take a few reads), so the end of a short
  // track can wait for it rather than leave.
  let nextLoad: Promise<ChildItem | null> | null = null;

  // The app's event stream ($lib/events, opened by the root layout) carries
  // cross-device resume sync (progress.updated: another device's position
  // while this player is paused) and the admin stop (playback.stop). The
  // player subscribes for this item's life; these unsubscribe.
  let eventSubscriptions: (() => void)[] = [];

  // What the bar shows: the pending scrub target while one is being picked,
  // else the playhead. Content time either way.
  const barMs = $derived(scrubTargetMs ?? position);

  // Trickplay scrub-preview state. Cues parsed from the WebVTT
  // index on mount; null when the item has no sprite sheets
  // (movies that haven't been processed yet, audio-only items).
  // The active cue follows the bar: the scrub target while scrubbing,
  // so the preview shows where OK will land. Cue times are content time.
  let trickplayCues = $state<TrickplayCue[]>([]);
  const trickplayCue = $derived(
    trickplayCues.length > 0 ? findCue(trickplayCues, barMs) : null,
  );

  // Online subtitle search overlay. Opened from the subtitle
  // picker via "Find more online…" — searches OpenSubtitles via
  // the server, lets the user download a pick, and reloads the
  // item so the new row (files[].external_subtitles) joins the
  // picker, selected (see downloadOnlineSubtitle).
  let onlineSubsOpen = $state(false);
  let onlineSubsLoading = $state(false);
  let onlineSubsResults = $state<OnlineSubtitle[]>([]);
  let onlineSubsCursor = $state(0);
  let onlineSubsError = $state('');
  let onlineSubsDownloading = $state(false);

  // Item types that have no video — keep controls (scrubber, title,
  // play/pause) visible permanently since there's no picture to dim
  // behind them.
  const isAudioItem = $derived(
    !!item && (
      item.type === 'track' ||
      item.type === 'audiobook' ||
      item.type === 'audiobook_chapter'
    )
  );
  // The now-playing view instead of a picture: the audio types, plus what
  // plays as audio only (a podcast's audio episode), which used to get a
  // black screen whose controls faded away.
  const nowPlaying = $derived(isAudioItem || isAudioOnly);
  const nowPlayingArtPath = $derived(item ? nowPlayingArt(item, parentPoster) : null);
  const queueText = $derived(queueLabel(item?.type, queuePosition, queueTotal));

  const speedAvailable = $derived(!!item && hasListeningSpeed(item.type) && !speedUnsupported);

  // Whether the server has an online subtitle search set up (navGates; the
  // player asks for the answer on mount). Closed until it says so.
  const onlineSubsAvailable = $derived($onlineSubtitlesGate);

  // The action row's buttons. Audio needs a session to re-issue (not the
  // file's direct stream); subtitles and chapters are for video only, and
  // Subtitles needs a track or the online search.
  const actions = $derived<PlayerAction[]>(
    playerActions({
      video: !!item && !isAudioItem && !isAudioOnly,
      switchableAudioTracks: isAudioOnly ? 0 : audioStreams.length,
      subtitleTracks: subtitleOptions.length,
      onlineSubtitles: onlineSubsAvailable,
      chapters: chapters.length,
    })
  );
  const actionsHint = $derived(actions.map((a) => ACTION_LABELS[a].toLowerCase()).join(' · '));

  // What CH ▲▼ do here (channelSkip), for the hints: '' when nothing. A
  // queue item's chapters stay on red / green, which the hints then name.
  const channelSteps = $derived(channelStep(item?.type, chapters.length));
  // The pointer's playback buttons (only while the pointer is on screen):
  // previous / next where CH ▲▼ would go somewhere (no previous on an
  // album's first track, no next in a film's last chapter).
  const transport = $derived<TransportButton[]>(
    transportButtons(
      transportSteps({
        step: channelSteps,
        prevSibling: !!prevSibling,
        nextSibling: !!nextSibling,
        chapterStarts: chapters.map((c) => c.start_ms),
        positionMs: scrubTargetMs ?? position,
      })
    )
  );
  const channelHint = $derived(
    channelSteps === 'item' ? `CH ▲▼ prev / next ${queueNoun(item?.type)}`
    : channelSteps === 'chapter' ? 'CH ▲▼ chapters'
    : ''
  );
  // Back on a showing Up Next card declines it; only the next Back leaves.
  const backHint = $derived(upNextShown && !!nextSibling && !upNextCovered() ? 'back cancel up next' : 'back exit');

  // The open picker as rows (audio, subtitles or chapters; one at a time),
  // drawn a window at a time around the cursor so a long list stays on the
  // panel. Subtitles: Off, the tracks, then "Find more online…" when the
  // server has an online search (action: true marks that row).
  const pickerTitle = $derived(
    audioPickerOpen ? 'Audio' : subtitlePickerOpen ? 'Subtitles' : chapterPickerOpen ? 'Chapters' : ''
  );
  // The chapter playing when the picker opened (its ● row).
  let chapterPickerCurrent = $state(-1);
  const pickerRows = $derived.by<PickerRow[]>(() => {
    if (audioPickerOpen) {
      return audioStreams.map((s, i) => ({ label: audioTrackLabel(s, i), current: i === activeAudioIndex }));
    }
    if (subtitlePickerOpen) {
      // The chosen track while it is still being fetched (a cold server
      // extraction can take minutes), and the one that gave up, say so.
      const status = (key: string) =>
        key === activeSubtitleKey && subtitleLoad === 'loading' ? ' · loading…'
        : key === subtitleFailedKey ? ' · unavailable'
        : '';
      return subtitlePickerRows(subtitleOptions, activeSubtitleKey, onlineSubsAvailable, status);
    }
    if (chapterPickerOpen) {
      return chapters.map((c, i) => ({ label: chapterRowLabel(c, i), current: i === chapterPickerCurrent }));
    }
    return [];
  });
  const pickerSpan = $derived(pickerWindow(pickerCursor, pickerRows.length));

  // The controls come up on any key or pointer move and fade 5 s after the
  // last one (Android's leanback timing), 8 s while the action row has
  // focus; never while paused, nor on the now-playing view (see
  // controlsHideDelayMs). Play re-arms the fade of controls a pause kept up.
  function showControls() {
    controlsVisible = true;
    if (controlsTimer) {
      clearTimeout(controlsTimer);
      controlsTimer = null;
    }
    const delay = controlsHideDelayMs({ alwaysOn: nowPlaying, paused, actionRowFocused: actionFocus >= 0 });
    if (delay !== null) controlsTimer = setTimeout(hideControls, delay);
  }

  function hideControls() {
    controlsTimer = null;
    // A picker opened from the row is up: keep the row (and its focus) for
    // when it closes.
    if (anyPickerOpen()) {
      showControls();
      return;
    }
    // Paused since the fade was armed: the controls stay (play re-arms it).
    if (paused) return;
    controlsVisible = false;
    actionFocus = -1;
  }

  function anyPickerOpen(): boolean {
    return audioPickerOpen || subtitlePickerOpen || chapterPickerOpen || onlineSubsOpen;
  }

  // The Up Next card (fixed, over everything) would sit on top of an open
  // picker, in the same corner, and take its clicks: it is hidden meanwhile,
  // its keys and countdown on hold.
  function upNextCovered(): boolean {
    return anyPickerOpen() || speedPickerOpen;
  }

  $effect(() => {
    if (nowPlaying) {
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

  // ←/→/◀◀/▶▶: move the pending scrub target, which commits through
  // seekToContent after a short idle (or on OK). Before the stream is ready
  // (an early press during "Starting playback…") the target simply waits
  // and loadedmetadata lands it. Nothing while an error is up (a re-issue
  // would play behind the overlay) or before the start point is known.
  function seek(deltaMs: number) {
    if (!video || error || refused || !positionKnown) return;
    scrub.nudge(deltaMs, position, duration);
    showControls();
  }

  // Seek to a content position. Every seek goes through here: scrubs,
  // chapters, Skip Intro / Credits. Inside what the session has produced it
  // sets currentTime (in stream time); before the session's head or past a
  // still-growing edge it re-issues the session at the target (ffmpeg is
  // paced at 1x, so the player can't outrun it, and the part before a
  // resume point was never transcoded); past the end of a finished window
  // it clamps to the end. A key seek (`relative`) only a little past a
  // growing edge clamps to it instead (Android's seekRelative never re-issues
  // forward): right after a (re)start the playlist lists a few segments while
  // ffmpeg's burst is seconds from writing the rest. Cross-device sync uses
  // the local-only syncSeekStreamSec instead. The decision is
  // planContentSeek ($lib/player/session), tested as the page runs it.
  function seekToContent(targetMs: number, relative = false) {
    if (!video || error || refused || leaving) return;
    showControls();
    const step = planContentSeek(targetMs, relative, {
      streamReady,
      opening: controller.opening,
      serverStream,
      durationMs: duration,
      offsetMs,
      window: streamWindow(),
    });
    switch (step.kind) {
      case 'park':
        // Not seekable yet, or a new session is on its way: hold the target;
        // loadedmetadata of the stream that is coming commits it.
        scrub.park(step.targetMs, duration, step.relative);
        return;
      case 'reissue':
        position = step.positionMs;
        reissueAt(step.positionMs);
        return;
      case 'seek':
        // Where a stream plays after a seek says nothing about its
        // container's clock: no shift is measured from it.
        ptsCaptureStartSec = null;
        video.currentTime = step.streamSec;
        position = step.contentMs;
        // The user put the playhead here (a seek to the very end ends the
        // item, see PlayheadWatch.userSeek).
        playhead.userSeek(step.contentMs);
        // The cue from where it was must not wait for the seek to finish
        // (see the 'seeking' listener).
        refreshCues();
        return;
    }
  }

  // What the live session has produced so far, in stream time: hls.js's
  // parsed playlist lists every segment ffmpeg has written (the element's
  // seekable range only covers hls.js's forward buffer, and its duration is
  // Infinity until ENDLIST). The native-HLS fallback reads the element.
  function streamWindow(): StreamWindow {
    if (hls) {
      const lvl = hls.levels?.[hls.currentLevel >= 0 ? hls.currentLevel : 0];
      return playlistWindow(lvl?.details) ?? { producedEndMs: 0, ended: false };
    }
    const v = video;
    if (v && serverStream && v.seekable.length > 0) {
      return {
        producedEndMs: Math.round(v.seekable.end(v.seekable.length - 1) * 1000),
        ended: Number.isFinite(v.duration),
      };
    }
    return { producedEndMs: 0, ended: false };
  }

  // The item's length in content time (the bar, the heartbeat and Up Next
  // divide by it): the listed length, never a growing session's partial
  // duration. See contentDurationMs.
  function refreshDuration() {
    const vd = video?.duration ?? 0;
    duration = contentDurationMs(item?.files[0], item, {
      playerDurationMs: Number.isFinite(vd) && vd > 0 ? vd * 1000 : 0,
      offsetMs,
      directAudio: !serverStream && isAudioOnly,
      ended: serverStream && streamWindow().ended,
    });
  }

  // Re-open the session at a content position outside the produced window
  // (Android's reissueAt). One in flight: while a start is out, seekToContent
  // parks later targets and the new session's loadedmetadata lands the last
  // one. Keeps the remux mode, the audio track and play/pause. The server's
  // Start retires the running session, so a failure is not "the old session
  // plays on": the controller retries once, then the error overlay shows.
  function reissueAt(targetMs: number) {
    if (!video || refused || error) return;
    if (controller.opening) {
      scrub.park(targetMs, duration);
      return;
    }
    void openStream({
      positionMs: targetMs,
      videoCopy: sessionVideoCopy,
      audioOrdinal: activeAudioIndex,
      ...intent.stamp(),
    });
  }

  // A start the server refused for a reason that ends playback: an admin
  // stopped this stream, or a parental watch limit was reached. Ends it with
  // the message and says true; anything else is the caller's to handle.
  function refusedByServer(e: unknown): boolean {
    if (isPlaybackStoppedError(e)) {
      endPlayback(stoppedTextFromServer(e.message));
      return true;
    }
    if (e instanceof ApiError && e.code === 'PARENTAL_LIMIT') {
      endPlayback(parentalBlockMessage(e.message));
      return true;
    }
    return false;
  }

  // A heartbeat the server refused: any 403 on a 'playing' beat ends
  // playback (Android's HeartbeatRefusal), with the matching sentence.
  function onHeartbeatRefused(r: HeartbeatRefusal) {
    if (r.kind === 'parental') endPlayback(parentalBlockMessage(r.reason));
    else if (r.kind === 'stopped') endPlayback(stoppedTextFromServer(r.message));
    else endPlayback(CONTENT_REVOKED_TEXT);
  }

  // play() returns a promise on these webviews; a refused autoplay rejects
  // it, which is not an error worth a console line per source.
  function playElement() {
    try {
      const p = video?.play();
      if (p && typeof p.catch === 'function') p.catch(() => {});
    } catch { /* element gone */ }
  }

  // Play or pause on the user's say-so. Before the stream is ready the
  // intent is recorded and loadedmetadata honours it (an attach keeps it
  // over the request's own autoplay, see PlayIntent).
  function setPlaying(play: boolean) {
    if (!video) return;
    intent.set(play);
    if (streamReady) {
      if (play) playElement();
      else video.pause();
    } else {
      // No element event will say so (a source being swapped in is already
      // paused without one): the bar shows the intent.
      paused = !play;
    }
    showControls();
  }

  function togglePlay() {
    if (!video) return;
    setPlaying(streamReady ? video.paused : !intent.playing);
  }

  // Chapter starts are content time. Measured from the bar (a pending
  // scrub target, if any, which the jump replaces) and seeked through
  // seekToContent, so a chapter before a resumed session's head re-issues
  // instead of landing on the wrong frame.
  function jumpToChapter(dir: 1 | -1) {
    if (chapters.length === 0 || !video) return;
    const from = scrubTargetMs ?? position;
    scrub.cancel();
    const idx = chapters.findIndex((c) => c.start_ms > from + 2000 * dir);
    // Next from the last chapter has nowhere to go (it used to wrap to the
    // first chapter, i.e. back to the start of the film).
    const target = dir === 1 ? idx : idx === -1 ? chapters.length - 1 : Math.max(0, idx - 1);
    const ch = target >= 0 ? chapters[target] : undefined;
    if (ch) seekToContent(ch.start_ms);
    else showControls();
  }

  // CH ▲ (dir 1) / CH ▼ (-1): the Magic Remote's next / previous, since it
  // has no ◀◀ ▶▶ and usually no colour keys (see channelStep). A track, a
  // book's chapter file or a podcast episode moves to the next / previous
  // item by the path ◀◀ ▶▶ take for a track; anything with chapters steps
  // them like green / red; anything else ignores the key. Nothing at the
  // ends of the queue (no wrap, and no seek in its place).
  function channelSkip(dir: 1 | -1) {
    switch (channelStep(item?.type, chapters.length)) {
      case 'item': {
        const target = dir === 1 ? nextSibling : prevSibling;
        if (target) void goToNext(target);
        else showControls();
        return;
      }
      case 'chapter':
        jumpToChapter(dir);
        return;
    }
  }

  // Single-owner teardown. Both the onMount cleanup and onDestroy (and
  // goToNext / stopAndLeave) can reach here; nulling the ref after
  // destroy makes repeat calls no-ops so the instance isn't double-
  // destroyed. A load retry still pending was for this instance.
  function destroyHls() {
    clearLoadRetry();
    if (hls) {
      hls.destroy();
      hls = null;
    }
  }

  // Fatal-error recovery for an hls.js instance: createHls wires it onto
  // every session (first start, re-issues, audio switches, resume, the
  // fallback), so each is equally hardened. The ladder itself is
  // $lib/player/recovery.ts: bounded load retries with backoff, a probe of a
  // dead session, the codec demotion, one decoder recovery, one remux →
  // transcode fallback, one re-init; then the error overlay.
  //
  // The budget is per instance (`recovery`), so one session's spent
  // recovery can't exhaust a later one's after an audio switch or resume.
  // The re-init instance inherits reinitAttempted so an unrecoverable
  // source can't loop full re-inits forever. A fragment that loads refills
  // the load-retry budget: a blip an hour later starts from scratch. A
  // playlist that never loaded (the server's 503 "playlist not ready") is
  // reloaded with loadSource(): startLoad() is a no-op before a manifest.
  //
  // One codec-demotion escalation per item. A fatal bufferAppendError is MSE
  // REFUSING THE BYTES — a codec rejection, not a buffer hiccup — and neither
  // recoverMediaError nor a full re-init of the SAME source can fix a codec
  // the decoder won't take. Ported from the web player: demote the claim
  // (persisted — a panel's hardware decode support never changes) and restart
  // the session so the server re-decides and hands back H.264.
  let codecEscalated = false;
  // One remux → transcode fallback per item (Android's fallbackFromDirectPlay).
  let remuxFallbackUsed = false;
  // The direct audio file failed to play here: every start from then on (the
  // fallback itself, a retry) is a server session (see directAudioFailure).
  let directAudioFailed = false;
  // hls.js has stalled before loadedmetadata on a fresh audio-only playlist
  // on this TV (see begin()), with no error to act on. A session for an
  // audio-only item that isn't ready by then ends in the error the direct
  // file got, not an endless "Starting playback…".
  const AUDIO_SESSION_START_TIMEOUT_MS = 45_000;
  let audioStartWatchdog: ReturnType<typeof setTimeout> | null = null;
  let loadRetryTimer: ReturnType<typeof setTimeout> | null = null;

  function clearAudioStartWatchdog() {
    if (audioStartWatchdog) {
      clearTimeout(audioStartWatchdog);
      audioStartWatchdog = null;
    }
  }

  function armAudioStartWatchdog(gen: number, atMs: number) {
    clearAudioStartWatchdog();
    audioStartWatchdog = setTimeout(() => {
      audioStartWatchdog = null;
      // Another stream bound since, or this one got going, or something
      // else already ended it.
      if (destroyed || gen !== attachGen || streamReady || error || refused || controller.suspended) return;
      showFatal('This TV can’t play this audio file.', { atMs });
    }, AUDIO_SESSION_START_TIMEOUT_MS);
  }

  function clearLoadRetry() {
    if (loadRetryTimer) {
      clearTimeout(loadRetryTimer);
      loadRetryTimer = null;
    }
  }

  function attachHlsErrorHandling(
    inst: InstanceType<typeof HlsType>,
    Hls: typeof HlsType,
    recovery: RecoveryState
  ) {
    inst.on(Hls.Events.FRAG_LOADED, () => {
      recovery.networkRetries = 0;
    });
    // A retried load that recovered while the forward buffer was still
    // playing fires no 'playing' / 'seeked' / 'canplay': drop the spinner
    // here once the element can play on (it froze Up Next's countdown too).
    inst.on(Hls.Events.FRAG_BUFFERED, () => {
      const v = video;
      if (!v) return;
      if (clearsSpinnerOnFragBuffered({
        buffering,
        liveInstance: inst === hls,
        streamReady,
        opening: controller.opening,
        starved: isStarved(v),
      })) {
        buffering = false;
      }
    });
    inst.on(Hls.Events.ERROR, (_event, data) => {
      console.warn('[HLS] error', data.type, data.details, data.fatal);
      // A replaced instance's last words, or a player already stopped.
      if (!data.fatal || inst !== hls || refused || leaving) return;
      if (controller.opening) {
        // A new stream is on its way, and the server's Start retired this
        // one's session already: its 404s are expected. Drop it quietly (an
        // error overlay now would also retire the new session on arrival).
        destroyHls();
        streamReady = false;
        buffering = true;
        return;
      }
      const kind: FatalKind =
        data.type === Hls.ErrorTypes.NETWORK_ERROR ? 'network'
        : data.type === Hls.ErrorTypes.MEDIA_ERROR ? 'media'
        : 'other';
      const details = String(data.details ?? 'unknown');
      const plan = planFatal(
        {
          kind,
          details,
          httpStatus: (data as { response?: { code?: number } }).response?.code,
          manifestLoaded: (inst.levels?.length ?? 0) > 0,
          // Only when the claim could have produced the stream: never for an
          // audio-only session (a failed direct audio file's fallback), whose
          // append error says nothing about the panel's video decoders.
          canDemoteCodec: !codecEscalated && supportsHEVC() && !isAudioOnly,
          canFallBackToTranscode: sessionVideoCopy && !remuxFallbackUsed,
        },
        recovery
      );
      applyPlan(plan, recovery);
      switch (plan.kind) {
        case 'demote': {
          codecEscalated = true;
          const codec = (item?.files?.[0]?.video_codec ?? '').toLowerCase();
          demoteCodec(codec === 'av1' ? 'av1' : 'hevc');
          console.warn('[HLS] bufferAppendError — codec rejected; demoting claim + restarting session');
          restartAfterDemotion();
          return;
        }
        case 'probe':
          void probeDeadStream(details);
          return;
        case 'retryLoad':
          // hls.js gave up on the load after its own retries; try again in
          // a moment, a bounded number of times. The spinner only if the
          // element is actually starved: the 30 s forward buffer often plays
          // on through a blip.
          if (video && isStarved(video)) buffering = true;
          clearLoadRetry();
          loadRetryTimer = setTimeout(() => {
            loadRetryTimer = null;
            if (inst === hls) inst.startLoad();
          }, plan.delayMs);
          return;
        case 'reloadManifest':
          // The playlist never loaded (ffmpeg still opening the source: the
          // server's 503 "playlist not ready"). Only loadSource() asks for a
          // manifest again; startLoad() did nothing and "Starting playback…"
          // stayed up for good. Bounded like any load retry.
          if (video && !loading && isStarved(video)) buffering = true;
          clearLoadRetry();
          loadRetryTimer = setTimeout(() => {
            loadRetryTimer = null;
            if (inst === hls && currentPlaylistUrl) inst.loadSource(currentPlaylistUrl);
          }, plan.delayMs);
          return;
        case 'recoverMedia':
          inst.recoverMediaError();
          return;
        case 'transcodeFallback':
          console.warn('[HLS] remux undecodable after recovery; falling back to a transcode');
          fallBackToTranscode();
          return;
        case 'reinit': {
          if (!currentPlaylistUrl || !video) break;
          // One full re-init of the same source before giving up, from where
          // it stopped (or where it was to start, if it never got going).
          // Same playlist, same offset: content time is unchanged.
          const at = video.currentTime > 0.5 ? video.currentTime : currentStartSec;
          destroyHls();
          streamReady = false;
          currentStartSec = at;
          createHls(Hls, currentPlaylistUrl, at, { ...freshRecovery(), reinitAttempted: true });
          return;
        }
      }
      showFatal(fatalText(kind, details));
    });
  }

  function fatalText(kind: FatalKind, details: string): string {
    if (kind === 'network') return `Playback error: lost the connection to the server (${details}).`;
    if (kind === 'media') return `Playback error: this TV could not decode the stream (${details}).`;
    return `Playback error: ${details}`;
  }

  // The session's playlist or segments answered 403 / 404: the stream is
  // gone. An admin stop or a refused heartbeat looks exactly like this to
  // hls.js, usually before the playback.stop event arrives (or instead of
  // it, when the event stream was down), and the paused player no longer
  // beats, so the heartbeat's 403 backstop would never run. Send one beat
  // now: refused → that message; otherwise the error overlay (OK retries).
  //
  // The probe can wait behind a hung heartbeat on the report lane, so the
  // stream it was about may be long replaced when it answers (an audio
  // switch, Home and back): a refusal still ends playback (it is about the
  // user and the item, not the stream), but the generic error is only for
  // the stream that died. Nothing is probed before the first frame (the
  // reporter sends no 'playing' then): that is the generic error. The
  // decision is probeOutcome ($lib/player/gates), tested.
  async function probeDeadStream(details: string) {
    const at = position;
    const gen = attachGen;
    // Whatever the answer, this instance has nothing left to load.
    destroyHls();
    streamReady = false;
    buffering = true;
    const refusal = reporter ? await reporter.probe(at, duration) : null;
    const outcome = probeOutcome({
      refused: !!refusal,
      // The playback.stop event may have landed meanwhile.
      ended: refused || leaving || destroyed,
      genAtStart: gen,
      genNow: attachGen,
      opening: controller.opening,
      suspended: controller.suspended,
      errorShown: !!error,
    });
    if (outcome === 'refusal' && refusal) onHeartbeatRefused(refusal);
    else if (outcome === 'fatal') showFatal(`Playback error: the server ended this stream (${details}).`, { atMs: at });
  }

  // ── Opening streams ────────────────────────────────────────────────
  //
  // Every open goes through `controller` ($lib/player/session-controller):
  // the first start, a seek outside the produced window (reissueAt), an
  // audio-track switch, the codec-demotion restart, the remux → transcode
  // fallback, the re-open after an app resume and the retry after an error.
  // One start in flight, the latest request queued behind it; a start that
  // comes back no longer wanted is retired; a failed one is retried once,
  // then the error overlay shows. They used to be four copies that each
  // dropped something (video_copy, the ordinal audio index, the session
  // offset), and two of them could race the server's last-writer-wins
  // supersede into leaving no live session at all.

  /** What a start produced: a server session, or the file's direct source
   *  (music, audiobooks, audio-only podcast episodes). */
  type Opened =
    | { kind: 'direct' }
    | { kind: 'hls'; session: TranscodeSession; Hls: typeof HlsType };

  const controller = new SessionController<Opened>({
    start: startStream,
    attach: attachOpened,
    retire: (o) => {
      if (o.kind === 'hls') retireSession(o.session);
    },
    canOpen: () => !destroyed && !leaving && !refused && !error && !!video && !!item,
    // Not worth a second try: the server said no, or the sign-in is gone.
    isRefusal: (e) =>
      isPlaybackStoppedError(e) ||
      e instanceof Unauthorized ||
      (e instanceof ApiError && e.code === 'PARENTAL_LIMIT'),
    failed: (e, req) => {
      console.warn('[player] start failed', e);
      buffering = false;
      if (e instanceof Unauthorized) {
        goto('#/login');
        return;
      }
      if (refusedByServer(e)) return;
      const msg = e instanceof Error && e.message ? e.message : 'unknown error';
      showFatal(`Playback error: could not start the stream (${msg}).`, {
        atMs: req.positionMs,
        videoCopy: req.videoCopy,
        audioOrdinal: req.audioOrdinal,
      });
    },
  });

  /** Ask the controller for a stream; shows the spinner while a start
   *  replaces a playing one. */
  function openStream(req: OpenRequest): Promise<OpenOutcome> {
    const r = { ...req, positionMs: Math.max(0, Math.round(req.positionMs)) };
    if (!loading) buffering = true;
    return controller.request(r).then(settleBuffering);
  }

  function settleBuffering<T extends OpenOutcome | null>(o: T): T {
    // 'attached' keeps it until the new source can play; 'superseded' hands
    // over to the request that replaced it.
    if (o !== 'attached' && o !== 'superseded') buffering = false;
    return o;
  }

  // The server side of an open. Direct audio needs none: its source is the
  // file itself, unless the TV couldn't play it (then the server's
  // audio-only session, see fallBackFromDirectAudio).
  async function startStream(req: OpenRequest): Promise<Opened> {
    const file = item?.files[0];
    if (!file) throw new Error('No playable file for this item.');
    if (isAudioOnly && !directAudioFailed) return { kind: 'direct' };
    // An out-of-range ordinal (a stale row after an item refresh) would earn
    // a 400; null leaves the track to the server's default (0:a:0).
    const ordinal =
      req.audioOrdinal !== null && validAudioOrdinal(req.audioOrdinal, audioStreams.length) ? req.audioOrdinal : null;
    const Hls = await loadHls();
    const fresh = await endpoints.transcode.start({
      itemId: itemID,
      // 2160, matching the Tizen client and this client's own
      // X-Client-Capabilities maxHeight. Asking for 1080 didn't just
      // cap 4K panels at 1080p — it forced the server to downscale-
      // TRANSCODE every 4K source instead of direct-playing it (the
      // decision tree reads the requested height as the client's
      // ceiling). The server mins it with the header's maxHeight, so a
      // panel that declares less gets less.
      height: 2160,
      positionMs: req.positionMs,
      fileId: file.id,
      videoCopy: req.videoCopy,
      // The ordinal, never AudioStream.index; explicit (including 0) on
      // every re-issue so the live track is kept. Omitted only for the
      // first start when no preferred audio language matched (and for a
      // file with no listed audio streams): the server's default track,
      // as on Android.
      audioStreamIndex: audioStreams.length > 0 && ordinal !== null ? ordinal : undefined,
      // Both explicit on every start: false after a runtime demotion.
      supportsHEVC: supportsHEVC(),
      supportsAV1: supportsAV1(),
    });
    return { kind: 'hls', session: fresh, Hls };
  }

  // Bind what a start produced to the element. The previous session (the
  // server has already superseded it) is retired and the player swapped
  // over; the bar holds the requested point until the new stream is ready.
  function attachOpened(o: Opened, req: OpenRequest) {
    const file = item?.files[0];
    if (!video || !file) {
      if (o.kind === 'hls') retireSession(o.session);
      return;
    }
    const old = session;
    session = o.kind === 'hls' ? o.session : null;
    if (old && old !== session) retireSession(old);
    destroyHls();
    attachGen++;
    bound = true;
    streamReady = false;
    // Plays unless a pause came in while this start was out.
    intent.forAttach(req);
    position = req.positionMs;
    // A new source hasn't played; its playhead starts where it was asked to.
    playhead.attach(req.positionMs);
    // A new stream clock: the cues wait for it (refreshCues), and the
    // container-shift latch is re-armed for an HLS session below.
    shownCues = [];
    ptsShiftMs = 0;
    ptsCaptureStartSec = null;
    if (o.kind === 'direct') {
      // Direct play: the file itself, so content time is media time and the
      // start point is a plain seek once metadata is in.
      serverStream = false;
      offsetMs = 0;
      currentPlaylistUrl = '';
      metadataSeekSec = req.positionMs > 0 ? req.positionMs / 1000 : null;
      reporter?.setDecision('directPlay');
      video.src = api.assetUrl(file.stream_url);
      return;
    }
    const fresh = o.session;
    offsetMs = sessionOffsetMs(fresh, req.positionMs);
    const gap = seg0GapMs(fresh);
    const startSec = hlsStartPositionSec(inStreamStartMs(req.positionMs, offsetMs, gap), gap);
    currentMinStartSec = gap / 1000;
    serverStream = true;
    sessionVideoCopy = req.videoCopy;
    activeAudioIndex =
      req.audioOrdinal !== null && validAudioOrdinal(req.audioOrdinal, audioStreams.length) ? req.audioOrdinal : 0;
    // Measured on this session's first 'playing' (see ptsShiftOnFirstPlayMs).
    ptsCaptureStartSec = startSec;
    reporter?.setDecision(req.videoCopy ? 'directStream' : 'transcode');
    const url = fresh.playlist_url.startsWith('http')
      ? fresh.playlist_url
      : api.mediaUrl(fresh.playlist_url);
    attachStream(o.Hls, url, startSec);
    if (isAudioOnly) armAudioStartWatchdog(attachGen, req.positionMs);
  }

  // Bind a session's playlist to the video element. hls.js starts it at
  // `startSec` itself (armFirstLoad; at most a segment earlier) — no
  // loadedmetadata seek, which used to apply the resume offset a second time
  // on top of the session's own. The native-HLS fallback has no
  // startPosition and seeks on metadata.
  function attachStream(Hls: typeof HlsType, url: string, startSec: number) {
    if (!video) return;
    currentPlaylistUrl = url;
    currentStartSec = startSec;
    streamReady = false;
    if (Hls.isSupported()) {
      metadataSeekSec = null;
      createHls(Hls, url, startSec, freshRecovery());
    } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
      metadataSeekSec = startSec > 0 ? startSec : null;
      video.src = url;
    } else {
      showFatal('HLS playback is not supported on this device.', { retryable: false });
    }
  }

  // The single hls.js construction site: every session (and the error
  // handler's last-resort re-init) gets the same config, ported from the
  // web client (see hlsSessionConfig) and the same error hardening.
  function createHls(
    Hls: typeof HlsType,
    url: string,
    startSec: number,
    recovery: RecoveryState
  ) {
    if (!video) return;
    const inst = new Hls(hlsSessionConfig(startSec));
    // `hls` first: the error handler ignores events from any other instance.
    hls = inst;
    attachHlsErrorHandling(inst, Hls, recovery);
    // hlsSessionConfig turns autoStartLoad off, so the session starts
    // loading once its first playlist is parsed and says what ffmpeg has
    // written (firstLoadPositionSec): a start at the edge of a growing
    // playlist begins at the start of the segment it falls in instead of
    // waiting a whole live reload with nothing on screen (~27 s to the first
    // frame resuming a 4K remux). How that start is made to stick against
    // hls.js's own autostart is armFirstLoad's ($lib/player/first-load,
    // tested against the real hls.js). A replay is only taken under
    // "Starting playback…" (`loading`: the first start, a retry, the codec
    // demotion): the bar isn't on screen, so moving the playhead back to
    // where the picture really starts can't be seen as a jump. A seek, a
    // chapter, Skip Intro or an audio switch re-issue lands exactly where it
    // was asked, as before.
    armFirstLoad(inst, Hls, {
      startSec,
      current: () => inst === hls,
      choose: (details) => (loading ? firstLoadPositionSec(startSec, details, currentMinStartSec) : startSec),
      onEarlyStart: (at) => {
        // Only once hls.js has kept the early start. Content time stays
        // stream + offset: the playhead is where the picture will start, so
        // the bar shows it from the first frame and never steps back once
        // it is up.
        console.info('[HLS] start', startSec.toFixed(3), 's lies at the playlist edge; starting at', at.toFixed(3), 's');
        position = toContentMs(at, offsetMs);
        playhead.at(position);
        // The container-shift measurement compares the first 'playing'
        // with where the stream really starts (ptsShiftOnFirstPlayMs).
        if (ptsCaptureStartSec !== null) ptsCaptureStartSec = at;
      },
    });
    inst.loadSource(url);
    inst.attachMedia(video);
  }

  // End a server session nothing reads any more. Fire-and-forget: the
  // server reaps it anyway, and a newer start may have superseded it.
  function retireSession(s: TranscodeSession) {
    if (!api.getToken()) return;
    void endpoints.transcode.stop(s.session_id, s.token).catch(() => {});
  }

  function endSession() {
    if (session) retireSession(session);
    session = null;
  }

  // Re-issue with the demoted claim: supportsHEVC()/supportsAV1() and the
  // capability header now say no, so the server lands on an H.264
  // transcode. video_copy is dropped on purpose — the bytes the panel just
  // refused may BE the stream-copied source video. Same position, audio
  // track and play state.
  function restartAfterDemotion() {
    if (!video || !item || !serverStream || refused) return;
    loading = true;
    // The doomed instance would keep firing fatals while the start is out.
    destroyHls();
    streamReady = false;
    void openStream({
      positionMs: position,
      videoCopy: false,
      // A track switch still on its way is the track the user wants.
      audioOrdinal: controller.intent?.audioOrdinal ?? activeAudioIndex,
      ...intent.stamp(),
    });
  }

  // A direct audio file the TV can't play: once, the same file through a
  // server session (the server's audio-only transcode, AAC in MPEG-TS) from
  // where it stopped, keeping the play state, as Android's
  // fallbackFromDirectPlay. Seeks and a retry after an error stay on
  // sessions from then on (directAudioFailed).
  function fallBackFromDirectAudio(atMs: number) {
    if (!video || !item || serverStream || refused) return;
    console.warn('[player] direct audio failed; falling back to a server transcode');
    directAudioFailed = true;
    void openStream({ positionMs: atMs, videoCopy: false, audioOrdinal: null, ...intent.stamp() });
  }

  // A remux the decoder can't take even after recovery: one restart as a
  // full transcode at the current content position, keeping the paused
  // state (Android's fallbackFromDirectPlay).
  function fallBackToTranscode() {
    if (!video || !item || !serverStream || refused) return;
    remuxFallbackUsed = true;
    destroyHls();
    streamReady = false;
    void openStream({
      positionMs: position,
      videoCopy: false,
      // A track switch still on its way is the track the user wants.
      audioOrdinal: controller.intent?.audioOrdinal ?? activeAudioIndex,
      ...intent.stamp(),
    });
  }

  // ── Ending playback ────────────────────────────────────────────────

  // Tear the stream down: no session, no decoder, no source.
  function teardownStream() {
    clearAudioStartWatchdog();
    streamReady = false;
    bound = false;
    buffering = false;
    endSession();
    destroyHls();
    try {
      video?.pause();
      if (video?.getAttribute('src')) {
        video.removeAttribute('src');
        video.load();
      }
    } catch { /* element already gone */ }
  }

  // The server ended playback (see `refused`): the SSE playback.stop event
  // (matched by isStopForPlayer), a 403 PLAYBACK_STOPPED / PARENTAL_LIMIT
  // from a start, or any 403 on a heartbeat (revoked). Stops at once and
  // shows the message; 'stopped' is reported once (the resume point is
  // kept), the transcode session ends and nothing opens again.
  function endPlayback(text: string) {
    if (refused) return;
    refused = true;
    controller.close();
    closePickers();
    closeOnlineSubtitleSearch();
    actionFocus = -1;
    speedPickerOpen = false;
    dismissUpNext();
    activeMarker = null;
    scrub.cancel();
    void reporter?.stopped(position, duration);
    teardownStream();
    unsubscribeEvents();
    // A subtitle still being fetched (it can poll for minutes) is dropped.
    dropSubtitleLoad();
    if (rateCheckTimer) { clearInterval(rateCheckTimer); rateCheckTimer = null; }
    if (controlsTimer) { clearTimeout(controlsTimer); controlsTimer = null; }
    paused = true;
    intent.set(false);
    loading = false;
    errorRetryable = false;
    error = text;
  }

  interface FatalOptions {
    /** Content position a retry re-opens at (default: the playhead). */
    atMs?: number;
    /** The failed stream's settings (default: the live session's). */
    videoCopy?: boolean;
    audioOrdinal?: number | null;
    /** False for what a retry can't change (default true). */
    retryable?: boolean;
  }

  // A playback failure: the error overlay, with the stream torn down so
  // nothing (a parked seek, a re-issue) plays behind it. OK / Play retries
  // (see retryAfterError) unless `retryable` is false.
  function showFatal(text: string, opts: FatalOptions = {}) {
    if (refused || leaving) return;
    retryReq = {
      positionMs: Math.max(0, Math.round(opts.atMs ?? position)),
      videoCopy: opts.videoCopy ?? sessionVideoCopy,
      // A first start that failed with no preferred track retries the same
      // way; anything else names its track.
      audioOrdinal: opts.audioOrdinal === undefined ? activeAudioIndex : opts.audioOrdinal,
      autoplay: true,
    };
    scrub.cancel();
    dismissUpNext();
    activeMarker = null;
    closePickers();
    closeOnlineSubtitleSearch();
    actionFocus = -1;
    speedPickerOpen = false;
    // Where the failure left it, for the detail page's Resume.
    if (bound && streamReady) reporter?.paused(retryReq.positionMs, duration);
    teardownStream();
    // A subtitle still being fetched (it can poll for minutes) stops with
    // the stream, not behind the message; a retry loads the track again.
    const subtitleWasLoading = subtitleLoad === 'loading';
    dropSubtitleLoad();
    if (subtitleWasLoading) subtitleLoad = 'idle';
    paused = true;
    loading = false;
    errorRetryable = opts.retryable ?? true;
    error = text;
  }

  // OK / Play on the error overlay: re-open where the failure left it, with
  // the same settings (Android's Play after a dismissed error). A load that
  // failed before it asked for a stream runs again from the top.
  function retryAfterError() {
    if (!errorRetryable || refused || leaving) return;
    error = '';
    errorRetryable = false;
    loading = true;
    // OK / Play is a play: a pause pressed during the retry's start still
    // wins over it (PlayIntent).
    intent.set(true);
    // The track whose load the failure dropped (see showFatal).
    if (activeSubtitleKey !== null && subtitleLoad === 'idle') void loadActiveSubtitle();
    if (!setupDone) {
      void begin();
      return;
    }
    void openStream({ ...retryReq, ...intent.stamp() });
  }

  // Done with this item for good (Back, the end, the next item): the final
  // report goes out once, and is waited for (capped by the reporter) so the
  // page we go to reads the saved position, not the last heartbeat's.
  async function finishPlayback() {
    leaving = true;
    scrub.cancel();
    if (upNextTimer) {
      clearInterval(upNextTimer);
      upNextTimer = null;
    }
    controller.close();
    clearLoadRetry();
    try {
      video?.pause();
    } catch { /* element gone */ }
    if (reporter) await reporter.stopped(position, duration);
    teardownStream();
  }

  // Back: return to the screen that launched playback (the album, the
  // season, the hub; nav.playItem pushed it). A cold deep link has no stack,
  // so it falls back to the item's own page.
  async function stopAndLeave() {
    if (leaving) return;
    await finishPlayback();
    if (!navigatesAfterWait({ destroyed })) return;
    goBack(`#/item/${itemID}`);
  }

  function onKey(k: RemoteKey): boolean {
    lastKeyAt = Date.now();
    // A key press makes the focus a hover put down the user's own.
    unhover = null;
    // On the way out: the final report is out, nothing more to do.
    if (leaving) return true;
    // Stopped by the server: the message stays up; Back leaves.
    if (refused) {
      if (k === 'back') void stopAndLeave();
      return true;
    }
    // The error overlay: Back leaves, OK / Play tries again from where it
    // stopped. Nothing else reaches the (torn-down) player behind it.
    if (error) {
      if (k === 'back') void stopAndLeave();
      else if (errorRetryable && (k === 'enter' || k === 'play' || k === 'playpause')) retryAfterError();
      return true;
    }
    // The Enter a Magic Remote click can send along with it: the click
    // already acted (opened a picker, picked a row).
    if (k === 'enter' && Date.now() - lastClickAt < POINTER_ECHO_MS) return true;
    // Audiobook speed picker (↑/↓ on the now-playing view).
    if (speedKey(k)) return true;
    // An open picker (audio, subtitles, chapters, the online search) grabs
    // keys before everything else: up/down move the cursor, OK picks, Back
    // closes.
    if (pickerKey(k)) return true;
    // A scrub target is pending: OK seeks there now, Back drops it (the
    // bar returns to the playhead). Ahead of Up Next and Skip so the
    // press goes to the scrub the user is in the middle of. Not during
    // "Starting playback…", where the bar isn't shown and Back must leave.
    if (scrubTargetMs !== null && !loading && (k === 'enter' || k === 'back')) {
      if (k === 'enter') scrub.flush();
      else scrub.cancel();
      showControls();
      return true;
    }
    // The Up Next card while it has focus (it takes it when it shows): ←/→
    // choose Play now / Cancel, OK presses, Down hands the keys back to the
    // player. Ahead of the action row: one of the two has focus at a time.
    // Not while a picker hides it (←/→ seek under the picker then).
    if (upNextShown && !upNextCovered() && upNextFocus >= 0 && onUpNextKey(k)) return true;
    // The action row ([Audio] [Subtitles] [Chapters]): Down from the
    // controls focuses it (with the controls hidden it only shows them),
    // ←/→ move, OK opens, ↑ / Back leave it. Ahead of Skip so OK / Back go
    // to the button the user is on.
    if (onActionRowKey(k)) return true;
    // The card without focus: Up returns to it, and Back declines it for
    // the rest of this play (the end of the episode then leaves instead of
    // advancing), focused or not.
    if (upNextShown && !upNextCovered() && onUpNextKey(k)) return true;
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
        setPlaying(true);
        return true;
      case 'pause':
      // The remote's Stop pauses (Android parity); Back is what leaves.
      case 'stop':
        setPlaying(false);
        return true;
      case 'left':
        seek(-10_000);
        return true;
      case 'right':
        seek(10_000);
        return true;
      case 'rewind':
        // Music tracks: rewind = previous track. Other audio /
        // video: rewind = 30 s seek.
        if (item?.type === 'track' && prevSibling) {
          void goToNext(prevSibling);
        } else {
          seek(-30_000);
        }
        return true;
      case 'forward':
        if (item?.type === 'track' && nextSibling) {
          void goToNext(nextSibling);
        } else {
          seek(30_000);
        }
        return true;
      case 'channelUp':
        channelSkip(1);
        return true;
      case 'channelDown':
        channelSkip(-1);
        return true;
      // ↑ / ↓ with nothing else to do (the action row, the Up Next card and
      // the speed picker had their turn above): bring up the controls, as
      // leanback's first d-pad press does. Up used to do nothing at all
      // while they were hidden. Not in actionRowKey: the row is asked before
      // the unfocused Up Next card, whose Up must still return to the card.
      case 'up':
      case 'down':
        showControls();
        return true;
      case 'green':
        jumpToChapter(1);
        return true;
      case 'red':
        jumpToChapter(-1);
        return true;
      case 'yellow':
        if (actions.includes('audio')) openAudioPicker();
        return true;
      case 'blue':
        if (actions.includes('subtitles')) openSubtitlePicker();
        return true;
    }
    return false;
  }

  // ── Audiobook speed ────────────────────────────────────────────────

  function openSpeedPicker() {
    closePickers();
    speedCursor = presetIndex(speed);
    speedPickerOpen = true;
  }

  function speedKey(k: RemoteKey): boolean {
    if (!speedPickerOpen) {
      // Only once the now-playing view is up (the picker lives inside it).
      if ((k === 'up' || k === 'down') && speedAvailable && !loading && !error && !anyPickerOpen()) {
        openSpeedPicker();
        return true;
      }
      return false;
    }
    const len = RATE_PRESETS.length;
    if (k === 'back') { speedPickerOpen = false; return true; }
    if (k === 'up') { speedCursor = (speedCursor - 1 + len) % len; return true; }
    if (k === 'down') { speedCursor = (speedCursor + 1) % len; return true; }
    if (k === 'enter') {
      speedPickerOpen = false;
      void chooseSpeed(RATE_PRESETS[speedCursor]);
      return true;
    }
    return false; // seek / play keys keep working under the picker
  }

  // The book's saved speed (a chapter resolves to its book; a book never set
  // starts at the user's latest speed). A server without the route (404) or
  // a non-book (422) leaves 1× — the control still works on this device.
  async function loadSpeed() {
    if (!item || !hasListeningSpeed(item.type)) return;
    try {
      const r = await endpoints.items.playbackRate(itemID);
      speed = clampRate(r.rate);
    } catch { /* keep 1× */ }
    applySpeed();
  }

  // Put the element at the right speed for the item: the book's speed for
  // audiobooks, 1× for everything else. Re-applied after loads / play since
  // a new src resets playbackRate.
  function applySpeed() {
    if (!video || refused) return;
    const target = item && hasListeningSpeed(item.type) && !speedUnsupported ? speed : 1;
    if (sameRate(video.playbackRate, target) && sameRate(video.defaultPlaybackRate, target)) return;
    const ok = applyMediaRate(video, target);
    rateCheck.clear();
    if (!ok && !sameRate(target, 1)) speedIgnored();
  }

  // The platform refused the rate (or plays at 1× regardless). Withdraw the
  // control on this TV for this play; the book's saved speed is left alone
  // for the user's other devices.
  function speedIgnored() {
    speedUnsupported = true;
    speedPickerOpen = false;
    if (video) applyMediaRate(video, 1);
  }

  async function chooseSpeed(rate: number) {
    speed = clampRate(rate);
    applySpeed();
    if (speedUnsupported) return;
    try {
      await endpoints.items.setPlaybackRate(itemID, speed);
    } catch { /* older server: the speed still applies on this device */ }
  }

  function startRateCheck() {
    if (rateCheckTimer) clearInterval(rateCheckTimer);
    rateCheckTimer = setInterval(() => {
      const v = video;
      if (!v || paused || loading || speedUnsupported || !item || !hasListeningSpeed(item.type) ||
          v.seeking || v.readyState < 3) {
        rateCheck.reset();
        return;
      }
      if (rateCheck.sample(performance.now(), v.currentTime * 1000, speed) === 'ignored') speedIgnored();
    }, 1000);
  }

  // ── Pickers and the action row ─────────────────────────────────────

  function openAudioPicker() {
    closePickers();
    speedPickerOpen = false;
    pickerCursor = activeAudioIndex < 0 ? 0 : activeAudioIndex;
    audioPickerOpen = true;
    showControls();
  }

  function openSubtitlePicker() {
    closePickers();
    speedPickerOpen = false;
    // Row 0 = "Off", rows 1..N = the options, then "Find more online…" when
    // the server has it (subtitlePickerRows).
    const at = subtitleOptions.findIndex((o) => o.key === activeSubtitleKey);
    pickerCursor = at < 0 ? 0 : at + 1;
    subtitlePickerOpen = true;
    showControls();
  }

  // Opens on the chapter playing (from the bar: a pending scrub target, if
  // any, which a pick replaces).
  function openChapterPicker() {
    if (chapters.length === 0) return;
    closePickers();
    speedPickerOpen = false;
    chapterPickerCurrent = chapterAt(chapters, scrubTargetMs ?? position);
    pickerCursor = Math.max(0, chapterPickerCurrent);
    chapterPickerOpen = true;
    showControls();
  }

  function closePickers() {
    audioPickerOpen = false;
    subtitlePickerOpen = false;
    chapterPickerOpen = false;
  }

  // A picker closed by the user (Back, or a pick): the controls come back
  // with the action row's focus where it was, so the next OK reopens it.
  function closePickerToRow() {
    closePickers();
    showControls();
  }

  function pickerKey(k: RemoteKey): boolean {
    // Online-subtitle overlay takes priority — runs its own cursor
    // separate from the local-track picker because it has its own
    // open/close lifecycle (search → results → download).
    if (onlineSubsOpen) return onlineSubsKey(k);
    if (!pickerTitle) return false;
    if (k === 'back') {
      closePickerToRow();
      return true;
    }
    // "Find more online…" is a row only on a server with an online subtitle
    // search (features.subtitles_external, navGates): elsewhere the search
    // answers 503 "not configured", so the row isn't offered there.
    const len = pickerRows.length;
    if (len === 0) return true;
    if (k === 'up') {
      pickerCursor = (pickerCursor - 1 + len) % len;
      return true;
    }
    if (k === 'down') {
      pickerCursor = (pickerCursor + 1) % len;
      return true;
    }
    if (k === 'enter') {
      choosePickerRow(pickerCursor);
      return true;
    }
    return false; // seek / play keys keep working under the picker
  }

  function choosePickerRow(i: number) {
    if (audioPickerOpen) {
      closePickerToRow();
      // The row number IS the ordinal the server takes.
      if (i !== activeAudioIndex) switchAudioStream(i);
    } else if (subtitlePickerOpen) {
      // Read before closing: the rows go with the picker.
      const online = !!pickerRows[i]?.action;
      closePickerToRow();
      if (online) {
        void openOnlineSubtitleSearch();
        return;
      }
      const opt = i === 0 ? null : subtitleOptions[i - 1];
      if (i === 0 || opt) selectSubtitle(opt ? opt.key : null, true);
    } else if (chapterPickerOpen) {
      closePickerToRow();
      const ch = chapters[i];
      if (!ch) return;
      // Content time, through seekToContent: a chapter before a resumed
      // session's head re-issues instead of landing on the wrong frame.
      scrub.cancel();
      seekToContent(ch.start_ms);
    }
  }

  // A picker row clicked with the pointer (the Magic Remote).
  function clickPickerRow(i: number, e: MouseEvent) {
    lastClickAt = Date.now();
    (e.currentTarget as HTMLElement | null)?.blur();
    if (!pickerTitle || i < 0 || i >= pickerRows.length) return;
    pickerCursor = i;
    choosePickerRow(i);
  }

  function openAction(a: PlayerAction) {
    if (a === 'audio') openAudioPicker();
    else if (a === 'subtitles') openSubtitlePicker();
    else openChapterPicker();
  }

  // A button clicked with the pointer: focus it and open its picker. The
  // focus is the user's now (it returns there when the picker closes).
  function clickAction(i: number, e: MouseEvent) {
    lastClickAt = Date.now();
    unhover = null;
    (e.currentTarget as HTMLElement | null)?.blur();
    const a = actions[i];
    if (!a || loading || error || refused || leaving) return;
    actionFocus = i;
    upNextFocus = -1;
    openAction(a);
  }

  // The pointer on a button moves the focus to it, so OK (whether the
  // Magic Remote sends it as the click or as an Enter) acts on the button
  // under the pointer; only while it is there (see `unhover`). The focus a
  // hover replaced comes back when the pointer leaves, unless a key or a
  // click took over meanwhile.
  function hoverFocus(set: () => void, isMine: () => boolean) {
    if (!pointerInUse()) return;
    // A hover still holding focus (no mouseleave seen) gives it back first.
    leaveHover();
    const prevRow = actionFocus;
    const prevCard = upNextFocus;
    set();
    unhover = () => {
      if (!isMine()) return;
      actionFocus = prevRow;
      upNextFocus = prevCard;
    };
  }

  function leaveHover() {
    const restore = unhover;
    unhover = null;
    restore?.();
  }

  function hoverAction(i: number) {
    if (anyPickerOpen()) return;
    hoverFocus(
      () => {
        actionFocus = i;
        upNextFocus = -1;
      },
      () => actionFocus === i && upNextFocus === -1,
    );
  }

  // The Up Next card's buttons: the same, and a click presses them.
  function hoverUpNext(i: number) {
    if (!upNextShown) return;
    hoverFocus(
      () => {
        upNextFocus = i;
        actionFocus = -1;
      },
      () => upNextFocus === i && actionFocus === -1,
    );
  }

  function clickUpNext(b: UpNextButton, e: MouseEvent) {
    lastClickAt = Date.now();
    unhover = null;
    (e.currentTarget as HTMLElement | null)?.blur();
    if (!upNextShown || loading || error || refused || leaving) return;
    pressUpNext(b);
  }

  // Likewise a picker row: OK then picks the row under the pointer, whether
  // the click or its Enter arrives first.
  function hoverPickerRow(i: number) {
    if (!pointerInUse() || !pickerTitle || i < 0 || i >= pickerRows.length) return;
    pickerCursor = i;
  }

  function pointerInUse(): boolean {
    return lastPointerMoveAt > lastKeyAt;
  }

  function onPointerMove() {
    lastPointerMoveAt = Date.now();
    showControls();
  }

  // ── The pointer's playback controls ────────────────────────────────
  //
  // LG's checklist wants play, pause, seek and previous / next reachable
  // with the screen cursor as well as the keys. While the pointer is on
  // screen ($pointerShown) the controls carry buttons for them
  // (actions.transportButtons) and a click on the bar seeks there; the
  // online subtitle results, the speed picker and Skip Intro / Credits take
  // clicks too. Each click is one press (lastClickAt: its Enter echo is
  // dropped).

  function clickTransport(b: TransportButton, e: MouseEvent) {
    lastClickAt = Date.now();
    unhover = null;
    (e.currentTarget as HTMLElement | null)?.blur();
    if (loading || error || refused || leaving) return;
    switch (b) {
      case 'prev':
        channelSkip(-1);
        return;
      case 'back':
        seek(-TRANSPORT_SKIP_MS);
        return;
      case 'playpause':
        togglePlay();
        return;
      case 'forward':
        seek(TRANSPORT_SKIP_MS);
        return;
      case 'next':
        channelSkip(1);
        return;
    }
  }

  // A click on the bar: there, through seekToContent like a chapter pick
  // (a point before a resumed session's head re-issues). Replaces a
  // pending scrub.
  function clickBar(e: MouseEvent) {
    lastClickAt = Date.now();
    if (loading || error || refused || leaving || !positionKnown) return;
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const target = barSeekMs(e.clientX, r.left, r.width, duration);
    if (target === null) return;
    scrub.cancel();
    seekToContent(target);
  }

  function clickSkipMarker(e: MouseEvent) {
    lastClickAt = Date.now();
    (e.currentTarget as HTMLElement | null)?.blur();
    if (activeMarker && !loading) skipMarker();
  }

  // An online subtitle result: the pointer's hover is the cursor, a click
  // downloads it (as OK on it does).
  function hoverOnlineSub(i: number) {
    if (!pointerInUse() || onlineSubsLoading || onlineSubsDownloading) return;
    if (i >= 0 && i < onlineSubsResults.length) onlineSubsCursor = i;
  }

  function clickOnlineSub(i: number, e: MouseEvent) {
    lastClickAt = Date.now();
    (e.currentTarget as HTMLElement | null)?.blur();
    if (!onlineSubsOpen || onlineSubsLoading || onlineSubsDownloading) return;
    const pick = onlineSubsResults[i];
    if (!pick) return;
    onlineSubsCursor = i;
    void downloadOnlineSubtitle(pick);
  }

  // The audiobook speed: the now-playing view's Speed button opens the
  // picker (↑ / ↓ do with the keys); a row is hovered and clicked like a
  // picker row.
  function clickSpeed(e: MouseEvent) {
    lastClickAt = Date.now();
    (e.currentTarget as HTMLElement | null)?.blur();
    if (!speedAvailable || loading || error || refused || leaving) return;
    if (speedPickerOpen) speedPickerOpen = false;
    else openSpeedPicker();
  }

  function hoverSpeedRow(i: number) {
    if (pointerInUse() && i >= 0 && i < RATE_PRESETS.length) speedCursor = i;
  }

  function clickSpeedRow(i: number, e: MouseEvent) {
    lastClickAt = Date.now();
    (e.currentTarget as HTMLElement | null)?.blur();
    const rate = RATE_PRESETS[i];
    if (!speedPickerOpen || rate === undefined) return;
    speedCursor = i;
    speedPickerOpen = false;
    void chooseSpeed(rate);
  }

  // The error overlay's buttons: OK's retry and Back's exit.
  function clickRetry(e: MouseEvent) {
    lastClickAt = Date.now();
    (e.currentTarget as HTMLElement | null)?.blur();
    if (error && errorRetryable && !leaving) retryAfterError();
  }

  function clickLeave(e: MouseEvent) {
    lastClickAt = Date.now();
    (e.currentTarget as HTMLElement | null)?.blur();
    void stopAndLeave();
  }

  // The remote on the action row (see actionRowKey). Only on video with the
  // player up and no picker open (a picker takes the arrows itself).
  function onActionRowKey(k: RemoteKey): boolean {
    if (nowPlaying || loading || anyPickerOpen() || speedPickerOpen) return false;
    // Hidden controls: the row isn't on screen, so its focus is gone too,
    // and Down only brings them up (a second Down focuses the row).
    const focus = controlsVisible ? actionFocus : -1;
    const step = actionRowKey(focus, actions.length, k, controlsVisible);
    switch (step.kind) {
      case 'reveal':
        showControls();
        return true;
      case 'focus':
        actionFocus = step.index;
        upNextFocus = -1;
        showControls();
        return true;
      case 'leave':
        actionFocus = -1;
        showControls();
        return true;
      case 'activate':
        openAction(actions[step.index]);
        return true;
      default:
        return false;
    }
  }

  // ── Online subtitle search ─────────────────────────────────────────

  async function openOnlineSubtitleSearch() {
    if (!onlineSubsAvailable) return;
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
    if (k === 'back') { closeOnlineSubtitleSearch(); showControls(); return true; }
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

  // Download a search result, then re-read the item: the new file lands in
  // files[].external_subtitles, the options rebuild (keys are stable, so the
  // current choice stays put) and the download is selected and shown. No
  // restart: the session carries no subtitles, so there is nothing to
  // re-issue (Android's reloadSubtitles; the web client auto-selects too).
  async function downloadOnlineSubtitle(pick: OnlineSubtitle) {
    if (!item || onlineSubsDownloading) return;
    const file = item.files[0];
    if (!file) return;
    onlineSubsDownloading = true;
    onlineSubsError = '';
    try {
      const before = subtitleOptions;
      const created = await endpoints.onlineSubtitles.download(itemID, file.id, pick);
      const refreshed = await endpoints.items.get(itemID);
      if (destroyed || leaving || refused) return;
      item = refreshed;
      const key = pickDownloadedSubtitle(before, subtitleOptions, pick.provider_file_id, created?.id);
      if (key) selectSubtitle(key, true);
      closeOnlineSubtitleSearch();
      showControls();
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

  // Re-issue the session at the current position with another audio
  // track. The server maps one audio track per session, so a language
  // switch can't go through hls.js's track selector: only a fresh session
  // carries it. `ordinal` is the track's 0-based position within
  // audio_streams (what the server's -map 0:a:N takes); anything out of
  // range is refused here rather than sent for a 400. Keeps the remux mode
  // (that of a start still in flight, which may be the transcode fallback),
  // the pending scrub target and the play state. The server's Start retires
  // the running session, so a failed switch can't just keep the old track:
  // the controller retries once, then the error overlay offers a retry.
  function switchAudioStream(ordinal: number) {
    if (!video || !item || !serverStream || refused || error) return;
    if (!validAudioOrdinal(ordinal, audioStreams.length)) {
      console.warn('[audio] refusing out-of-range track', ordinal, 'of', audioStreams.length);
      return;
    }
    const pending = controller.intent;
    if (ordinal === (pending?.audioOrdinal ?? activeAudioIndex)) return;
    const target = scrubTargetMs ?? position;
    scrub.cancel();
    void openStream({
      positionMs: target,
      videoCopy: pending?.videoCopy ?? sessionVideoCopy,
      audioOrdinal: ordinal,
      ...intent.stamp(),
    });
  }

  // ── Subtitle loading and drawing ───────────────────────────────────

  // Show a track (by option key), or none (null). `explicit` is the user's
  // pick, which the saved preference never overrides afterwards. Picking the
  // track already showing does nothing, unless its load failed (a retry).
  function selectSubtitle(key: string | null, explicit: boolean) {
    if (explicit) subtitleChosen = true;
    if (key === activeSubtitleKey && subtitleLoad !== 'failed') return;
    activeSubtitleKey = key;
    void loadActiveSubtitle();
  }

  // A track the server is still extracting from the source (a cold 4K remux
  // can take minutes) keeps loading: loadSubtitleCues polls it on until it
  // is cached, and the picker reads "loading…" meanwhile. One that gives up
  // falls back to Off with "Subtitles unavailable"; picking it again retries.
  async function loadActiveSubtitle() {
    const gen = ++subtitleGen;
    abortSubtitleFetch();
    cues = [];
    shownCues = [];
    hideSubtitleNotice();
    const key = activeSubtitleKey;
    const opt = key === null ? undefined : subtitleOptions.find((o) => o.key === key);
    if (!opt) {
      subtitleLoad = 'idle';
      return;
    }
    subtitleLoad = 'loading';
    const r = await loadSubtitleCues(
      () => fetchSubtitleText(opt.path),
      () => gen === subtitleGen && !destroyed,
      // The asset token in the URL can expire mid-film: refresh, re-sign.
      { onUnauthorized: () => api.refreshTokens() }
    );
    if (r.kind === 'stale') return;
    if (r.kind === 'failed') {
      subtitleLoad = 'failed';
      subtitleFailedKey = key;
      // Nothing of it ever showed: the picker's ● goes back to Off.
      activeSubtitleKey = null;
      showSubtitleNotice('Subtitles unavailable');
      return;
    }
    subtitleLoad = 'ready';
    if (subtitleFailedKey === key) subtitleFailedKey = null;
    cues = r.cues;
    refreshCues();
  }

  // GET the track's WebVTT (asset token in the URL; CORS is open, and a
  // plain GET stays a "simple" request from the app's file:// origin). Each
  // try may wait SUBTITLE_FETCH_TIMEOUT_MS: the server answers a cold
  // extraction only once it is done. A newer pick aborts it.
  async function fetchSubtitleText(path: string): Promise<string> {
    const ctrl = typeof AbortController !== 'undefined' ? new AbortController() : null;
    subtitleFetch = ctrl;
    const timer = ctrl ? setTimeout(() => ctrl.abort(), SUBTITLE_FETCH_TIMEOUT_MS) : null;
    try {
      const res = await fetch(api.assetUrl(path), { signal: ctrl?.signal });
      // With the server's error code: its 504 means the extraction failed.
      if (!res.ok) throw await subtitleFetchError(res);
      return await res.text();
    } finally {
      if (timer) clearTimeout(timer);
      if (subtitleFetch === ctrl) subtitleFetch = null;
    }
  }

  function abortSubtitleFetch() {
    const c = subtitleFetch;
    subtitleFetch = null;
    try {
      c?.abort();
    } catch { /* already settled */ }
  }

  // Leaving or stopped: a load still out (it can poll for minutes) is
  // dropped, its request with it.
  function dropSubtitleLoad() {
    subtitleGen++;
    abortSubtitleFetch();
    hideSubtitleNotice();
  }

  // Put the cues for the element's clock on screen (content time, less a
  // measured container shift). Runs on timeupdate and as a seek starts and
  // ends; nothing while no stream is ready (a source being swapped in has
  // no clock yet) or the element has no data at its position (mid-seek).
  function refreshCues() {
    const v = video;
    if (!v || cues.length === 0 || !streamReady || v.readyState < 2) {
      if (shownCues.length > 0) shownCues = [];
      return;
    }
    const next = activeCues(cues, subtitleClockMs(v.currentTime, offsetMs, ptsShiftMs));
    if (!sameCues(next, shownCues)) shownCues = next;
  }

  // The notice shows for 5 s of picture: the overlay is hidden during
  // "Starting playback…", so its clock starts once that clears
  // (armSubtitleNotice from loadedmetadata).
  function showSubtitleNotice(text: string) {
    subtitleNotice = text;
    if (subtitleNoticeTimer) {
      clearTimeout(subtitleNoticeTimer);
      subtitleNoticeTimer = null;
    }
    if (!loading) armSubtitleNotice();
  }

  function armSubtitleNotice() {
    if (!subtitleNotice || subtitleNoticeTimer) return;
    subtitleNoticeTimer = setTimeout(hideSubtitleNotice, 5000);
  }

  function hideSubtitleNotice() {
    if (subtitleNoticeTimer) {
      clearTimeout(subtitleNoticeTimer);
      subtitleNoticeTimer = null;
    }
    subtitleNotice = '';
  }

  // The user's saved track preferences, read once per load (one GET):
  // preferred_audio_lang → the ordinal the FIRST start maps (only when a
  // track matches; otherwise the server's default), preferred_subtitle_lang
  // + forced_subtitles_only → a subtitle among the embedded AND downloaded
  // rows (pickPreferredSubtitle, the web client's contract), shown from the
  // start. A choice made in this session wins over both. Best-effort: no
  // preferences, no auto-selection. Video only (direct audio has no track to
  // pick and no subtitles), and fetched alongside the watch-limit check and
  // the play decision rather than ahead of them (see begin).
  async function loadPreferences() {
    let prefs: UserPreferences;
    try {
      prefs = await endpoints.users.preferences();
    } catch {
      return;
    }
    if (destroyed || leaving || refused) return;
    preferredAudioOrdinal = pickPreferredAudio(audioStreams, prefs.preferred_audio_lang);
    // Subtitles are for video; music and books have their own view. Nor
    // behind the error overlay (Dolby Vision is refused while this is out):
    // the load would poll on for minutes under the message.
    if (nowPlaying || subtitleChosen || error) return;
    const pick = pickPreferredSubtitle(
      subtitleOptions,
      prefs.preferred_subtitle_lang,
      prefs.forced_subtitles_only ?? false,
    );
    if (pick) selectSubtitle(pick.key, false);
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

  // Marker bounds are content time: seek through seekToContent (the end of
  // a long intro can lie past what a fresh session has produced).
  function skipMarker() {
    const m = activeMarker;
    if (!m || !video) return;
    dismissedMarkers.add(m.start_ms);
    activeMarker = null;
    seekToContent(Math.max(0, m.end_ms));
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

  // How long the end of an item waits for a next-item lookup still running.
  const NEXT_LOOKUP_WAIT_MS = 3_000;

  // Look up what plays after this item (resolveNext in
  // $lib/player/siblings): the next episode, rolling into the next season;
  // the next track, rolling into the artist's next album; the next chapter
  // of the book, numbered or not. Movies, standalone tracks and single-file
  // books have none. The parent's listing also gives the previous track and
  // "Track N of M".
  function loadNextSibling() {
    const it = item;
    if (!it || !it.parent_id) return;
    if (
      it.type !== 'episode' &&
      it.type !== 'track' &&
      it.type !== 'audiobook_chapter' &&
      it.type !== 'podcast_episode'
    ) {
      return;
    }
    const parentId = it.parent_id;
    nextLoad = (async () => {
      let kids: ChildItem[] | undefined;
      try {
        kids = await endpoints.items.children(parentId);
        const s = findSiblings(kids, it);
        prevSibling = s.prev;
        if (s.position > 0) {
          queuePosition = s.position;
          queueTotal = s.total;
        }
      } catch {
        // Best-effort; resolveNext reads the listing again.
      }
      const next = await resolveNext(
        it,
        (id) => endpoints.items.children(id),
        (id) => endpoints.items.get(id),
        kids
      );
      nextSibling = next;
      return next;
    })();
  }

  // The parent context, off the critical path and best-effort:
  //  - an episode: its show and season number, for "Show · S2 · E5" over
  //    the title (an episode filed straight under its show has no season);
  //  - a track: its album (title, cover) and artist;
  //  - a chapter / a podcast episode: its book / podcast (title, cover).
  // The cover stands in for an item with no art of its own on the
  // now-playing view (nowPlayingArt): the album's fetch used to throw it away.
  async function loadParentContext() {
    const it = item;
    if (!it || !it.parent_id) return;
    try {
      if (it.type === 'episode') {
        const [parent, grand] = await Promise.all([
          endpoints.items.get(it.parent_id),
          it.grandparent_id ? endpoints.items.get(it.grandparent_id).catch(() => null) : Promise.resolve(null),
        ]);
        const season = parent.type === 'season' ? parent : null;
        let show = parent.type === 'show' ? parent : grand;
        // A server that doesn't send grandparent_id: up from the season.
        if (!show && season?.parent_id) show = await endpoints.items.get(season.parent_id).catch(() => null);
        if (!destroyed) episodeContext = episodeContextLine(show?.title, season?.index, it.index);
        return;
      }
      if (!usesParentCover(it.type)) return;
      const parent = await endpoints.items.get(it.parent_id);
      if (destroyed) return;
      albumTitle = parent.title ?? '';
      parentPoster = parent.poster_path ?? null;
      if (it.type === 'track' && parent.parent_id) {
        const artist = await endpoints.items.get(parent.parent_id);
        if (!destroyed) artistTitle = artist.title ?? '';
      }
    } catch {
      // Best-effort.
    }
  }

  function maybeShowUpNext() {
    // A seek back out of the last stretch takes the card down again, or its
    // countdown would still advance mid-episode. Not a decline: it returns
    // when playback gets back there.
    if (upNextShown && duration > 0 && duration - position > UP_NEXT_LEAD_MS) {
      dismissUpNext();
      return;
    }
    if (!nextSibling || upNextShown || upNextDeclined || duration <= 0) return;
    // Music tracks + audiobook chapters chain silently at EOS —
    // skip the overlay entirely so the outro/closing-line plays
    // through.
    if (item?.type === 'track' || item?.type === 'audiobook_chapter') return;
    if (duration - position > UP_NEXT_LEAD_MS) return;

    upNextShown = true;
    upNextCountdown = UP_NEXT_COUNTDOWN_SEC;
    if (upNextTimer) clearInterval(upNextTimer);
    upNextTimer = setInterval(() => {
      // A pause holds the countdown: whoever catches the card and pauses
      // has asked to stay (Android's upNextCountdownTicks). So does a
      // rebuffer or a re-issue, and a picker that hides the card: it never
      // runs out unseen.
      if (!intent.playing || !streamReady || buffering || upNextCovered()) return;
      upNextCountdown -= 1;
      if (upNextCountdown <= 0 && nextSibling) void goToNext(nextSibling);
    }, 1000);
    // Play now takes focus, as on Android, unless the user is busy in the
    // action row or a picker: then the card waits for ↑.
    upNextFocus = actionFocus < 0 && !anyPickerOpen() && !speedPickerOpen ? 0 : -1;
  }

  // Hide the card (an error, a refusal); not a decline.
  function dismissUpNext() {
    upNextShown = false;
    upNextFocus = -1;
    if (upNextTimer) {
      clearInterval(upNextTimer);
      upNextTimer = null;
    }
  }

  // The remote on the card (see upNextKey).
  function onUpNextKey(k: RemoteKey): boolean {
    if (!nextSibling) return false;
    const step = upNextKey(upNextFocus, k);
    switch (step.kind) {
      case 'focus':
        upNextFocus = step.index;
        actionFocus = -1;
        return true;
      case 'release':
        upNextFocus = -1;
        showControls();
        return true;
      case 'press':
        pressUpNext(step.button);
        return true;
      default:
        return false;
    }
  }

  function pressUpNext(b: UpNextButton) {
    if (b === 'play' && nextSibling) void goToNext(nextSibling);
    else declineUpNext();
  }

  // Back on the card: no Up Next for the rest of this play, and the end of
  // the episode leaves instead of advancing.
  function declineUpNext() {
    upNextDeclined = true;
    dismissUpNext();
  }

  // The end of the item: report it once, then move on. A track or a chapter
  // chains silently to the next one (there is no card to decline); an
  // episode advances unless Up Next was declined. Nothing next (a movie, a
  // series finale, a book's last chapter): back to the launching screen.
  // Only the end of the stream that is playing: while a re-issue is out the
  // old session plays on from its buffer, and its end (a chapter jump back
  // from the last seconds, say) marked the item watched and advanced
  // instead of landing the jump. A source being swapped in isn't ready yet.
  // And only a real end: an 'ended' from a stream that stopped short (a
  // track that started, never got going and "ended" ~30 s later) is
  // re-opened once from where it really was, then the error overlay; it is
  // never reported watched or chained past (endedAction, $lib/player/gates).
  async function onEnded() {
    // A finished session's own end, in content time (see endedAction).
    const w = serverStream ? streamWindow() : null;
    const end = endedAction({
      streamReady,
      opening: controller.opening,
      refused,
      leaving,
      destroyed,
      errorShown: !!error,
      played: playhead.played,
      lastMs: playhead.lastMs,
      durationMs: duration,
      streamEndMs: w && w.ended && w.producedEndMs > 0 ? offsetMs + w.producedEndMs : null,
      retriedAtMs: falseEndRetryAtMs,
    });
    if (end.kind === 'ignore') return;
    if (end.kind !== 'complete') {
      // The timeupdate just before this 'ended' set `position` from the
      // element's clock, which reads the duration once a stream has ended,
      // early or not. Left there, the bar sat at 100% and every later report
      // (Back from the error overlay, Back / Home during the retry's start)
      // sent stopped at the full length, marking the item watched: the very
      // outcome this check exists to prevent. Put both back where it was.
      position = end.atMs;
      playhead.at(end.atMs);
      // The same ended clock may have brought up the Up Next card; left up,
      // its countdown would advance to the next episode 10 s into the retry.
      // Not a decline: it comes back when playback really gets there.
      dismissUpNext();
      recoverFromEarlyEnd(end);
      return;
    }
    void reporter?.stopped(duration, duration);
    const next =
      nextSibling ?? (nextLoad ? ((await settleWithin(nextLoad, NEXT_LOOKUP_WAIT_MS)) ?? null) : null);
    if (!navigatesAfterWait({ destroyed, refused, leaving })) return;
    const chainsSilently = item?.type === 'track' || item?.type === 'audiobook_chapter';
    if (next && (chainsSilently || !upNextDeclined)) void goToNext(next);
    else void stopAndLeave();
  }

  // A stream that 'ended' short of the item's end. Logged with the
  // element's state (what produced it on the C1 is still unconfirmed), then
  // the same stream re-opened once where the playhead really was, keeping
  // the play state, as any re-issue does; a second early end shows the
  // error overlay, whose OK retries from there. A server session re-opens
  // clear of the item's end, as a seek's re-issue does.
  function recoverFromEarlyEnd(end: { kind: 'retry' | 'fail'; atMs: number }) {
    console.warn('[player] the stream ended early:', JSON.stringify({
      action: end.kind,
      atMs: end.atMs,
      durationMs: duration,
      played: playhead.played,
      serverStream,
      media: mediaSnapshot(video),
    }));
    if (end.kind === 'fail' || !video || !item) {
      showFatal(`Playback error: the stream stopped early, at ${fmt(end.atMs)}.`, { atMs: end.atMs });
      return;
    }
    falseEndRetryAtMs = end.atMs;
    const at = serverStream && duration > 0
      ? Math.min(end.atMs, Math.max(0, duration - REISSUE_END_GUARD_MS))
      : end.atMs;
    void openStream({
      positionMs: at,
      videoCopy: serverStream ? sessionVideoCopy : false,
      audioOrdinal: serverStream ? activeAudioIndex : null,
      ...intent.stamp(),
    });
  }

  // What the element says about itself, for a log line from the TV.
  function mediaSnapshot(v: HTMLVideoElement | undefined) {
    if (!v) return null;
    const ranges: string[] = [];
    try {
      for (let i = 0; i < v.buffered.length; i++) {
        ranges.push(`${v.buffered.start(i).toFixed(2)}-${v.buffered.end(i).toFixed(2)}`);
      }
    } catch { /* no ranges */ }
    return {
      currentTime: v.currentTime,
      duration: v.duration,
      readyState: v.readyState,
      networkState: v.networkState,
      paused: v.paused,
      ended: v.ended,
      error: v.error?.code ?? null,
      buffered: ranges,
      src: hls ? 'hls.js' : v.currentSrc ? 'element' : 'none',
    };
  }

  // Player → player (Up Next, the next / previous track): the final report
  // for this item, then the next one REPLACES this route, so Back from it
  // returns to the screen that launched playback, never to the item just
  // played (and an album of tracks doesn't stack one route per track). The
  // next item starts from the top, as on Android (newInstance(next.id, 0)):
  // at its saved resume point, a track skipped a minute in earlier started
  // a minute in when the album played through.
  async function goToNext(target: ChildItem) {
    if (leaving) return;
    await finishPlayback();
    if (!navigatesAfterWait({ destroyed })) return;
    replaceTo(`#/watch/${target.id}`, { id: target.id, ms: 0 });
  }

  // ── Cross-device sync and the admin stop ───────────────────────────
  //
  // Both arrive on the app's event stream ($lib/events), which the root
  // layout keeps open (reconnecting with backoff, refreshing its token only
  // when the stored one looks spent). The player only subscribes, for as
  // long as it plays this item.

  function subscribeEvents() {
    unsubscribeEvents();
    eventSubscriptions = [
      events.subscribe(PROGRESS_UPDATED_EVENT, onProgressEvent),
      events.subscribe(PLAYBACK_STOP_EVENT, onStopEvent),
    ];
  }

  function unsubscribeEvents() {
    for (const off of eventSubscriptions) off();
    eventSubscriptions = [];
  }

  // Admin stop. The channel is per user, so act only when it targets this
  // player: this item, and our transcode session or our client name (the
  // one the heartbeats carry) when the event names one. A stop naming
  // another of the user's devices is not ours.
  function onStopEvent(evt: NotificationEvent) {
    const stop = parsePlaybackStop(evt.data);
    if (stop && isStopForPlayer(stop, {
      itemId: item ? itemID : null,
      sessionId: session?.session_id ?? null,
      clientName: clientName(),
    })) {
      endPlayback(adminStopText(stop.message));
    }
  }

  // Another of the user's devices reported progress on this item: while
  // this player is paused, snap to it so resuming picks up where the other
  // device left off.
  function onProgressEvent(evt: NotificationEvent) {
    if (evt.item_id !== itemID) return;
    const reported = evt.data?.position_ms;
    if (!video || !reported || error || refused || leaving) return;
    // Only honour the sync when the local player is paused — if
    // the user is actively watching here, *this* device is the
    // authoritative position. Nor mid-scrub or mid-swap.
    if (intent.playing || !streamReady || controller.opening || scrubTargetMs !== null) return;
    // The reported position is CONTENT time; on a session it maps to
    // stream time through the offset (writing it raw seeked a resumed
    // session to position + offset). Within 2 s of where we are is our own
    // heartbeat echoed back; outside the loaded session it's left alone,
    // since sync never re-issues a session behind the user's back
    // (Android parity).
    const w = serverStream
      ? { offsetMs, durationMs: duration, ...streamWindow() }
      : { offsetMs: 0, durationMs: duration, producedEndMs: duration, ended: true };
    const sec = syncSeekStreamSec(reported, position, w);
    if (sec === null) return;
    ptsCaptureStartSec = null;
    video.currentTime = sec;
    position = toContentMs(sec, w.offsetMs);
    playhead.at(position);
  }

  // ── App suspend / resume ───────────────────────────────────────────
  //
  // webOS holds a single hardware video decoder per app. Leaving it bound
  // while the app is backgrounded (Home pressed, an overlay app opened)
  // can leave the decoder wedged on return, so we release the player on
  // background and re-acquire on foreground. visibilitychange covers the
  // common case; webOSRelaunch fires on some firmwares when the app is
  // re-launched while resident.
  //
  // The controller keeps what to re-open (see SuspendSnapshot): a start in
  // flight or queued resumes as that start (its target and play state); a
  // live stream at a pending scrub target, else the playhead; and Home
  // pressed while the item was still loading leaves it to the start's own
  // request, which then waits for the return. That last case used to save
  // the initial 0 and resume a film at 0:00.

  function releasePlayerForSuspend() {
    if (controller.suspended || refused || leaving) return;
    const live = bound && !error;
    if (live && streamReady) reporter?.paused(position, duration);
    controller.suspend({
      live,
      positionMs: position,
      scrubTargetMs: scrub.value,
      wantPlaying: intent.playing,
      intentSeq: intent.seq,
      videoCopy: sessionVideoCopy,
      audioOrdinal: activeAudioIndex,
    });
    scrub.cancel();
    // Drop the transcode session + decoder (and a direct source with it):
    // re-opened on return rather than kept running for a paused client.
    // teardownStream clears streamReady before pausing, so the pause isn't
    // taken for the user's.
    teardownStream();
  }

  function reacquirePlayerAfterResume() {
    if (!controller.suspended) return;
    if (!loading && !error) buffering = true;
    void controller.resume().then(settleBuffering);
  }

  function onVisibilityChange() {
    if (document.hidden) releasePlayerForSuspend();
    else reacquirePlayerAfterResume();
  }

  // Map a server PARENTAL_LIMIT reason to a friendly sentence for the
  // block overlay. Mirrors the web + phone clients.
  function parentalBlockMessage(reason: string): string {
    if (reason === 'outside_allowed_hours')
      return 'Outside the allowed hours for this account. Try again during the permitted times.';
    if (reason === 'daily_limit_reached')
      return "Today's watch-time limit for this account has been reached. Check back tomorrow.";
    return 'Playback is blocked by a parental watch limit on this account.';
  }

  // What the heartbeat reports: the position while actually playing,
  // nothing otherwise (paused, rebuffering, a start or re-issue on its way,
  // an error up), which skips the beat.
  function heartbeatState(): HeartbeatState {
    if (!streamReady || paused || loading || error || refused || leaving) return null;
    if (controller.opening || controller.suspended) return null;
    return { positionMs: position, durationMs: duration };
  }

  // The element's listeners, wired once per mount. They serve every source
  // the element gets: direct audio, the first session and every re-issue.
  function wireVideoEvents(v: HTMLVideoElement) {
    v.addEventListener('loadedmetadata', () => {
      // Don't autoplay into a backgrounded app — the suspend path has
      // already released the player; resume re-binds or re-issues.
      if (controller.suspended || refused || leaving || !bound) return;
      // hls.js starts a session at its startPosition itself; only direct
      // audio and the native-HLS fallback need a seek here.
      if (metadataSeekSec !== null) v.currentTime = metadataSeekSec;
      metadataSeekSec = null;
      streamReady = true;
      applySpeed();
      loading = false;
      // Play unless the user means it paused (a seek or audio switch made
      // while paused). hls.js's media recovery re-attaches and fires this
      // again; the same intent applies.
      if (intent.playing) playElement();
      // A swapped-in source starts paused without a 'pause' event.
      else paused = true;
      // A scrub made while the stream was starting or being swapped in
      // lands now (unless presses are still coming; the timer has it).
      if (scrubTargetMs !== null && !scrub.counting) scrub.flush();
      showControls();
      // A subtitle that gave up during "Starting playback…": its notice
      // shows from now.
      armSubtitleNotice();
    });
    v.addEventListener('timeupdate', () => {
      // Subtitles follow the stream that is actually playing, the old one
      // included while a re-issue is out (it plays on from its buffer).
      refreshCues();
      // Hold the bar while a session is being opened or the new one hasn't
      // reached its start yet: a new source resets the element's clock to
      // 0 before hls.js seeks to startPosition, and reading it then put
      // (and reported) the stream's head as the position.
      if (!streamReady || controller.opening || v.readyState < 2) return;
      position = toContentMs(v.currentTime, offsetMs);
      // Where the player really is, for onEnded (not the clock an early
      // end sets; see PlayheadWatch).
      playhead.tick(position, v);
      refreshDuration();
      // Marker watcher: surface a "Skip Intro" / "Skip Credits"
      // overlay while the playhead is inside an active window,
      // unless the user has already dismissed that marker.
      updateActiveMarker();
      // Up Next watcher: 25 s before EOS, pop the overlay so
      // the user can hit Enter to skip the credits early or
      // let the countdown auto-advance.
      maybeShowUpNext();
    });
    v.addEventListener('pause', () => {
      paused = true;
      // Only a pause of a ready stream is the user's (or the system's): a
      // source swap, a suspend, an error or the way out pause it too. The
      // end of the stream reports 'stopped' itself. After a refusal
      // 'stopped' was already reported; a late 'paused' would put the
      // stream back on Now Playing.
      if (!streamReady || refused || leaving || controller.suspended || v.ended) return;
      intent.set(false);
      reporter?.paused(position, duration);
      showControls();
    });
    v.addEventListener('play', () => {
      paused = false;
      if (streamReady) intent.set(true);
      applySpeed();
      // Controls a pause kept up start fading again.
      if (controlsVisible && !controlsTimer) showControls();
    });
    v.addEventListener('playing', () => {
      buffering = false;
      // Played at least once: from now on leaving reports the position.
      reporter?.arm();
      // This source got going: its end can be the item's (see onEnded).
      if (streamReady) playhead.playing();
      // A session's FIRST 'playing' only (the web client's one-shot latch):
      // measured on a later play, a pause position became the "shift".
      if (ptsCaptureStartSec !== null && streamReady) {
        ptsShiftMs = ptsShiftOnFirstPlayMs({ offsetMs, startSec: ptsCaptureStartSec, currentSec: v.currentTime });
        ptsCaptureStartSec = null;
        refreshCues();
      }
    });
    // Rebuffering: a seek, a thin buffer. Cleared once it can play again.
    v.addEventListener('waiting', () => {
      rateCheck.reset();
      if (streamReady) buffering = true;
    });
    v.addEventListener('seeking', () => {
      // Whoever seeked (the user, hls.js's start or a gap skip), the next
      // timeupdate may land anywhere: not a clock jump.
      playhead.seeking();
      if (streamReady) buffering = true;
      // Chromium fires timeupdate and seeked only once a seek completes, so
      // the cue from before a seek stayed up for the whole rebuffer (4-5 s
      // on a local seek into unbuffered media). currentTime already reads
      // the target here: this shows the target's cue, or clears the overlay
      // while the element has no data there (refreshCues).
      refreshCues();
    });
    // A seek or a refill jumps the media clock; start a fresh speed window.
    v.addEventListener('seeked', () => {
      rateCheck.reset();
      buffering = false;
      refreshCues();
    });
    v.addEventListener('canplay', () => {
      buffering = false;
    });
    // hls.js reports its own failures (attachHlsErrorHandling). This is the
    // direct audio source and the native-HLS fallback, which otherwise left
    // "Starting playback…" up for good. A direct-play stream the server
    // refused after an admin stop surfaces only as a media error too: ask
    // the server what it was.
    v.addEventListener('error', () => {
      // Not while a new stream is on its way: the old one is going anyway.
      if (hls || refused || leaving || controller.suspended || controller.opening || error || !bound) return;
      const src = v.currentSrc || v.getAttribute('src') || '';
      if (!src) return;
      if (serverStream) {
        showFatal('Playback error: this TV could not play the stream.');
        return;
      }
      const code = v.error?.code;
      const at = position;
      void probePlaybackStopped(src).then((text) => {
        if (refused || leaving || error || destroyed) return;
        if (text) {
          endPlayback(text);
          return;
        }
        // Not a stop: the connection, or a file this TV can't play (one
        // server transcode first, see directAudioFailure).
        switch (directAudioFailure(code, directAudioFailed)) {
          case 'network':
            showFatal('Playback error: lost the connection to the server.', { atMs: at });
            return;
          case 'transcode':
            fallBackFromDirectAudio(at);
            return;
          default:
            showFatal('This TV can’t play this audio file.', { atMs: at });
        }
      });
    });
    v.addEventListener('ended', () => {
      void onEnded();
    });
  }

  // Load the item and open its first stream. Re-run by OK on the error
  // overlay when a load failed before it asked for a stream.
  async function begin() {
    loading = true;
    try {
      const it = await endpoints.items.get(itemID);
      item = it;
      if (it.type === 'book') {
        // Ebooks need a paginated reader, not the video pipeline.
        // Defence-in-depth — the item detail page already hides the
        // Play button for book type, but a stray deep link or older
        // build elsewhere could still land us here.
        showFatal('Book reading isn’t available on TV. Open this book in the web or phone app.', { retryable: false });
        return;
      }
      const file = it.files[0];
      if (!file) {
        showFatal('No playable file for this item.', { retryable: false });
        return;
      }

      // Resume point, unless the caller asked for another start ("Watch
      // again" / the show's up-next episode — see playItem in nav.ts).
      const startMs = startOverride ?? it.view_offset_ms ?? 0;
      // Content time from here on: the bar, a scrub during "Starting
      // playback…" and the start request all measure from it. Set before
      // the awaits below, so nothing reads the initial 0 meanwhile.
      position = startMs;
      positionKnown = true;
      refreshDuration();

      // Audio-only items skip the HLS transcode path. The TV's
      // media element plays MP3/AAC/M4A/M4B/FLAC directly, and
      // hls.js's audio-only handling can stall before loadedmetadata
      // fires on a freshly-spawned audio playlist — hence the
      // "Starting playback…" hang. Direct play also avoids needless
      // transcode load on the server.
      //
      // Prefer item.type to detect — "audiobook (Illustrated)"
      // editions sometimes include a real per-chapter slideshow
      // video stream, so the codec heuristic alone misclassifies
      // them. For podcasts the type can host video too, so we keep
      // the codec check there.
      const audioType =
        it.type === 'track' ||
        it.type === 'audiobook' ||
        it.type === 'audiobook_chapter';
      isAudioOnly = audioType || (!file.video_codec && !!file.audio_codec);

      // The saved audio / subtitle languages (one small GET), for video
      // only: direct audio picks no track and shows no subtitles, and an
      // album played through paid a round trip per track for nothing. In
      // flight alongside the watch-limit check and the play decision, and
      // awaited before the start, whose request carries the preferred audio
      // track. The preferred subtitle starts loading as soon as it's known.
      const prefsLoad = isAudioOnly ? null : loadPreferences();

      // Parental watch-limit pre-flight — block a restricted user before
      // any stream/transcode starts. Fail-open if the check itself errors;
      // the progress heartbeat below still catches a cap reached mid-session.
      try {
        const wl = await endpoints.users.watchLimit();
        if (!wl.allowed) {
          endPlayback(parentalBlockMessage(wl.reason ?? ''));
          return;
        }
      } catch { /* limit lookup failed — fail open, allow playback */ }

      if (!sideLoadsStarted) {
        sideLoadsStarted = true;
        // Markers + next-sibling load in parallel with the playback
        // session — neither is on the critical path; failures are
        // best-effort (an empty marker list just means no Skip
        // button, no Up Next means natural EOS exits).
        void loadMarkers();
        loadNextSibling();
        void loadParentContext();
        void loadTrickplay();
      }

      // Left during the awaits above: bind nothing (a detached element
      // would still play direct audio).
      if (destroyed || leaving || refused || !video) return;

      let videoCopy = false;
      if (isAudioOnly) {
        // Audiobooks: the book's speed (fetched in the background, applied
        // whenever it lands) and the check that the TV honours it.
        if (hasListeningSpeed(it.type) && !rateCheckTimer) {
          void loadSpeed();
          startRateCheck();
        }
      } else {
        // Server-authoritative play decision (capability profiles): the server
        // uses our X-Client-Capabilities header. directPlay/directStream → the
        // server stream-copies the video (hls.js plays it); transcode → full
        // re-encode. Falls back to full transcode if the call fails.
        let verdict: string | null = null;
        try {
          verdict = (await endpoints.transcode.decide(itemID, file.id)).decision;
        } catch (e) {
          console.warn('[capability] playback-decision failed, full transcode:', e);
        }
        // Dolby Vision is not supported — the server returns the "unsupported"
        // verdict (DV can't be tonemapped correctly server-side; see
        // docs/dolby-vision.md). Show a clear message instead of a broken
        // transcode. The hdr_type check covers a failed/absent decision call.
        if (verdict === 'unsupported' ||
            (file.hdr_type ?? '').toLowerCase() === 'dolby_vision') {
          showFatal('Dolby Vision is not supported', { retryable: false });
          return;
        }
        videoCopy = verdict ? verdict !== 'transcode' : false;
        sessionVideoCopy = videoCopy;
      }
      // Never rejects (best-effort).
      if (prefsLoad) await prefsLoad;
      if (destroyed || leaving || refused) return;

      // The heartbeat runs from now on; it beats only while playing (see
      // heartbeatState), and nothing terminal is reported before the
      // player has played (reporter.arm on 'playing').
      if (!reporter) {
        reporter = new ProgressReporter({
          itemId: itemID,
          send: (r) =>
            endpoints.items.progress(r.itemId, r.positionMs, r.durationMs, r.state, {
              clientName: r.clientName,
              decision: r.decision,
              fileId: r.fileId,
            }),
          clientName: clientName(),
          fileId: file.id,
          onRefused: onHeartbeatRefused,
        });
        reporter.start(heartbeatState);
      }

      setupDone = true;
      // Backgrounded meanwhile: the controller holds this request until the
      // return (see releasePlayerForSuspend). A failure shows the error
      // overlay through the controller's failed().
      // Plays unless the user paused during "Starting playback…".
      await openStream({ positionMs: startMs, videoCopy, audioOrdinal: preferredAudioOrdinal, ...intent.stamp() });
    } catch (e) {
      if (e instanceof Unauthorized) goto('#/login');
      else if (!refusedByServer(e)) {
        const msg = e instanceof Error && e.message ? e.message : 'unknown error';
        showFatal(`Couldn’t load this item (${msg}).`);
      }
    }
  }
  let sideLoadsStarted = false;

  onMount(() => {
    // The element, for the cleanup (bind:this may be cleared by then).
    const el = video;
    const offKey = focusManager.pushKeyHandler(onKey);
    document.addEventListener('visibilitychange', onVisibilityChange);
    window.addEventListener('webOSRelaunch', onVisibilityChange);
    // Element listeners go on before any source is bound, so the first
    // loadedmetadata can't slip past them.
    if (video) wireVideoEvents(video);
    // Sync and the admin stop, from the app's event stream. The handlers
    // wait for the item and a ready stream themselves.
    subscribeEvents();
    // Whether the server has an online subtitle search (Subtitles > "Find
    // more online…"); a no-op when the top nav already asked. A player
    // opened straight from "play on this TV" may be the first to.
    void ensureNavGates();

    void begin();

    return () => {
      // A start still in flight sees this and retires its session.
      destroyed = true;
      controller.close();
      offKey();
      document.removeEventListener('visibilitychange', onVisibilityChange);
      window.removeEventListener('webOSRelaunch', onVisibilityChange);
      scrub.cancel();
      // Latched: a no-op after Back / the end / the next item reported it.
      void reporter?.stopped(position, duration);
      endSession();
      destroyHls();
      // A direct source (a track, a book) is released with the page, as
      // teardownStream releases it on Back and the next item. A page torn
      // down any other way (a "play on this TV" transfer, the epoch remount)
      // left its detached element holding the file, and on webOS the media
      // pipeline with it, while the next player's element started; and its
      // late events still reached this page's handlers (onEnded ignores a
      // destroyed page too). streamReady first, so that pause isn't taken
      // for the user's (as in teardownStream).
      streamReady = false;
      try {
        el?.pause();
        if (el?.getAttribute('src')) {
          el.removeAttribute('src');
          el.load();
        }
      } catch { /* element already gone */ }
      clearAudioStartWatchdog();
      if (controlsTimer) clearTimeout(controlsTimer);
      if (upNextTimer) clearInterval(upNextTimer);
      if (rateCheckTimer) { clearInterval(rateCheckTimer); rateCheckTimer = null; }
      // A subtitle load still out is dropped (its generation is stale).
      dropSubtitleLoad();
      unsubscribeEvents();
    };
  });

  // Defence-in-depth: destroyHls() is idempotent (nulls the ref), so this
  // is a no-op if the onMount cleanup already ran. Single-owner teardown.
  onDestroy(() => {
    destroyed = true;
    scrub.cancel();
    destroyHls();
    clearAudioStartWatchdog();
    unsubscribeEvents();
    if (upNextTimer) clearInterval(upNextTimer);
    if (rateCheckTimer) { clearInterval(rateCheckTimer); rateCheckTimer = null; }
  });

  // The bar's fill (and the trickplay preview's anchor) follow barMs: the
  // pending scrub target while scrubbing, else the playhead.
  const progressPct = $derived(duration > 0 ? Math.min(100, (barMs / duration) * 100) : 0);
</script>

<!-- Music / audiobook / podcast-audio view — full-screen "now playing"
     panel. Renders for what plays as audio only; video items fall through
     to the .player overlay controls below. The art falls back to the
     parent's cover (an album's, a book's, a podcast's). -->
{#if nowPlaying && !loading && !error && item}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="music-view" onmousemove={onPointerMove}>
    <div class="music-content">
      {#if nowPlayingArtPath}
        <img class="music-art" src={api.assetUrl(`/artwork/${nowPlayingArtPath}?w=720`)} alt="" />
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
        {#if queueText}
          <span>{queueText}</span>
        {:else if item.year}
          <span>{item.year}</span>
        {/if}
        <span>{paused ? '❚❚ Paused' : '▶ Playing'}</span>
        {#if speedAvailable}
          <span class="music-speed">Speed {formatRate(speed)}</span>
        {/if}
      </div>
      {#if speedUnsupported && hasListeningSpeed(item.type)}
        <div class="music-note">This TV can only play this book at normal speed.</div>
      {/if}
      <div class="music-bar">
        <div class="music-elapsed" class:scrubbing={scrubTargetMs !== null}>{fmt(barMs)}</div>
        <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
        <div class="music-track" class:clickable={$pointerShown} data-pointer-target onclick={clickBar}>
          <div class="music-fill" style="width: {progressPct}%"></div>
          {#each chapters as ch (ch.start_ms)}
            {#if duration > 0}
              <div class="music-chapter-marker" style="left: {(ch.start_ms / duration) * 100}%"></div>
            {/if}
          {/each}
        </div>
        <div class="music-remaining">-{fmt(duration - barMs)}</div>
      </div>
      {#if $pointerShown}
        <!-- The pointer's buttons (clickTransport): the keys' OK, ← →
             and CH ▲▼, and the speed picker's ↑ ↓. -->
        <div class="transport">
          {#each transport as b (b)}
            <button type="button" tabindex="-1" class="transport-button" onclick={(e) => clickTransport(b, e)}>
              {transportLabel(b, paused)}
            </button>
          {/each}
          {#if speedAvailable}
            <button type="button" tabindex="-1" class="transport-button" class:active={speedPickerOpen} onclick={clickSpeed}>
              Speed {formatRate(speed)}
            </button>
          {/if}
        </div>
      {/if}
      <div class="music-hints">
        {#if speedPickerOpen}
          <!-- The speed picker takes ↑ ↓ / OK / Back ahead of everything
               (speedKey); ← → still seek under it. -->
          <span>↑ ↓ choose</span>
          <span>OK select</span>
          <span>back close</span>
        {:else if scrubTargetMs !== null}
          <!-- A pending scrub: it lands on its own after a moment. -->
          <span>OK seek now</span>
          <span>← → keep moving</span>
          <span>back cancel</span>
        {:else}
          <span>OK play / pause</span>
          <span>← → seek 10s</span>
          {#if item.type === 'track'}
            <!-- ◀◀ ▶▶ step tracks too; the Magic Remote only has CH. -->
            <span>CH ▲▼ / ◀◀ ▶▶ prev / next track</span>
          {:else}
            <span>◀◀ ▶▶ seek 30s</span>
            {#if channelHint}<span>{channelHint}</span>{/if}
          {/if}
          {#if channelSteps === 'item' && chapters.length > 0}<span>red/green chapters</span>{/if}
          {#if speedAvailable}<span>↑ ↓ speed</span>{/if}
          <span>{backHint}</span>
        {/if}
      </div>
    </div>
    <!-- Speed picker: ↑/↓ opens it, ↑/↓ moves, OK picks, Back closes.
         Lives inside .music-view — the view sits above .player, so a
         picker there would be hidden behind it. -->
    {#if speedPickerOpen}
      <div class="picker speed-picker">
        <div class="picker-title">Speed</div>
        {#each RATE_PRESETS as r, i (r)}
          <button
            type="button"
            tabindex="-1"
            class="picker-button picker-row"
            class:active={i === speedCursor}
            class:current={sameRate(r, speed)}
            onclick={(e) => clickSpeedRow(i, e)}
            onmouseenter={() => hoverSpeedRow(i)}
          >{#if sameRate(r, speed)}{'● '}{/if}{formatRate(r)}</button>
        {/each}
      </div>
    {/if}
  </div>
{/if}

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="player" onmousemove={onPointerMove}>
  <!-- svelte-ignore a11y_media_has_caption -->
  <video bind:this={video} class="video" class:hidden={nowPlaying} playsinline></video>

  <!-- Subtitles, drawn from the chosen track's WebVTT against the content
       clock (see Subtitles in the script): over the video, raised clear of
       the controls while they show. Text only: nothing from the file is
       rendered as markup. -->
  {#if !nowPlaying && !loading && !error && (shownCues.length > 0 || subtitleNotice)}
    <div class="subtitle-overlay" class:raised={controlsVisible}>
      {#each shownCues as cue}
        <div class="subtitle-cue">
          {#each cue.text.split('\n') as line}<span class="subtitle-line">{line}</span>{/each}
        </div>
      {/each}
      {#if subtitleNotice}<div class="subtitle-notice">{subtitleNotice}</div>{/if}
    </div>
  {/if}

  {#if loading}
    <div class="overlay center">
      <div class="title">Starting playback…</div>
      {#if item}<div class="sub">{item.title}</div>{/if}
    </div>
  {:else if error}
    <div class="overlay center">
      <div class="title error">{error}</div>
      <div class="sub">{errorRetryable ? 'OK to try again · Back to exit' : 'Back to exit'}</div>
      {#if $pointerShown}
        <div class="transport overlay-buttons">
          {#if errorRetryable}
            <button type="button" tabindex="-1" class="transport-button" onclick={clickRetry}>Try again</button>
          {/if}
          <button type="button" tabindex="-1" class="transport-button" onclick={clickLeave}>Exit</button>
        </div>
      {/if}
    </div>
  {/if}

  <!-- Skip Intro / Skip Credits overlay. Shown while playhead is
       inside an active marker window; OK skips, Back dismisses
       (key handling lives in onKey above); the pointer clicks it. -->
  {#if activeMarker && !loading}
    <button type="button" tabindex="-1" class="skip-marker" onclick={clickSkipMarker}>
      Press OK to skip {activeMarker.kind === 'credits' ? 'Credits' : 'Intro'}
    </button>
  {/if}

  <!-- The audio / subtitle / chapter picker: opened from the action row
       (OK) or the colour keys (yellow audio, blue subtitles). ↑/↓ move the
       cursor, OK picks, Back closes (focus goes back to the button); the
       pointer clicks a row. Audio re-issues the session with the row's
       ordinal; subtitles load the track into .subtitle-overlay; a chapter
       seeks through seekToContent. A long list scrolls with the cursor. -->
  {#if pickerTitle}
    <div class="picker" class:picker-wide={chapterPickerOpen}>
      <div class="picker-title">{pickerTitle}</div>
      {#if pickerSpan.start > 0}<div class="picker-more">▲</div>{/if}
      {#each pickerRows.slice(pickerSpan.start, pickerSpan.end) as row, j (pickerSpan.start + j)}
        <button
          type="button"
          tabindex="-1"
          class="picker-button picker-row"
          class:picker-row-action={row.action}
          class:active={pickerSpan.start + j === pickerCursor}
          class:current={row.current}
          onclick={(e) => clickPickerRow(pickerSpan.start + j, e)}
          onmouseenter={() => hoverPickerRow(pickerSpan.start + j)}
        >{#if row.current}{'● '}{/if}{row.label}</button>
      {/each}
      {#if pickerSpan.end < pickerRows.length}<div class="picker-more">▼</div>{/if}
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
          <button
            type="button"
            tabindex="-1"
            class="picker-button picker-row"
            class:active={onlineSubsCursor === i}
            onclick={(e) => clickOnlineSub(i, e)}
            onmouseenter={() => hoverOnlineSub(i)}
          >
            <span class="online-sub-line">
              <span class="online-sub-lang">{r.language || 'und'}</span>
              <span class="online-sub-name">{r.file_name}</span>
            </span>
            <span class="online-sub-meta">
              {#if r.from_trusted}<span>trusted</span>{/if}
              {#if r.hd}<span>hd</span>{/if}
              {#if r.hearing_impaired}<span>SDH</span>{/if}
              {#if r.download_count}<span>{r.download_count.toLocaleString()} dl</span>{/if}
              {#if r.uploader_name}<span>by {r.uploader_name}</span>{/if}
            </span>
          </button>
        {/each}
      {/if}
      {#if onlineSubsDownloading}
        <div class="picker-row">Downloading…</div>
      {/if}
    </div>
  {/if}

  {#if !nowPlaying && controlsVisible && !loading && !error}
    <div class="controls">
      <div class="top">
        {#if episodeContext}<div class="now-playing-context">{episodeContext}</div>{/if}
        {#if item}<div class="now-playing">{item.title}</div>{/if}
      </div>

      <div class="bottom">
        <div class="state">
          {#if scrubTargetMs !== null}
            ⇆ seek to {fmt(scrubTargetMs)}
          {:else}
            {paused ? '❚❚ paused' : '▶ playing'}
          {/if}
        </div>
        <div class="bar">
          <div class="elapsed" class:scrubbing={scrubTargetMs !== null}>{fmt(barMs)}</div>
          <!-- With the pointer up, a click on the bar seeks there (clickBar);
               data-pointer-target has the focus manager leave OK over it to
               that click. -->
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
          <div class="track" class:clickable={$pointerShown} data-pointer-target onclick={clickBar}>
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
                   bar's position (the scrub target while scrubbing)
                   regardless of surrounding layout. -->
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
          <div class="remaining">{fmt(duration - barMs)}</div>
        </div>

        {#if $pointerShown}
          <!-- The pointer's buttons (clickTransport): the keys' OK, ← → and
               CH ▲▼. Only while the pointer is on screen; the D-pad has
               the keys. -->
          <div class="transport">
            {#each transport as b (b)}
              <button type="button" tabindex="-1" class="transport-button" onclick={(e) => clickTransport(b, e)}>
                {transportLabel(b, paused)}
              </button>
            {/each}
          </div>
        {/if}

        <!-- On-screen actions: Down focuses the row, ←/→ move, OK opens the
             picker; the pointer clicks them. The dot is the colour key that
             opens the same picker. -->
        {#if actions.length > 0}
          <div class="actions">
            {#each actions as a, i (a)}
              <button
                type="button"
                tabindex="-1"
                class="action"
                class:focused={i === actionFocus}
                onclick={(e) => clickAction(i, e)}
                onmouseenter={() => hoverAction(i)}
                onmouseleave={leaveHover}
              >
                {#if a === 'audio'}<span class="key-dot key-yellow"></span>{:else if a === 'subtitles'}<span class="key-dot key-blue"></span>{/if}{ACTION_LABELS[a]}
              </button>
            {/each}
          </div>
        {/if}

        <div class="hints">
          {#if pickerTitle || onlineSubsOpen}
            <!-- An open picker takes ↑ ↓ / OK / Back ahead of everything,
                 a pending scrub's OK and Back included (see pickerKey). -->
            <span>↑ ↓ choose</span>
            <span>OK select</span>
            <span>back close</span>
          {:else if scrubTargetMs !== null}
            <!-- A pending scrub: it lands on its own after a moment. -->
            <span>OK seek now</span>
            <span>← → ◀◀ ▶▶ keep moving</span>
            <span>back cancel</span>
          {:else if actionFocus >= 0}
            <span>← → choose</span>
            <span>OK open</span>
            <span>↑ back to playback</span>
          {:else}
            <span>← → seek 10s</span>
            <span>◀◀ ▶▶ seek 30s</span>
            <span>OK play/pause</span>
            {#if channelHint}<span>{channelHint}</span>{/if}
            {#if channelSteps === 'item' && chapters.length > 0}<span>red/green chapters</span>{/if}
            {#if actions.length > 0}<span>↓ {actionsHint}</span>{/if}
            <span>{backHint}</span>
          {/if}
        </div>
      </div>
    </div>
  {/if}
</div>

<!-- Up Next card — appears 25 s before EOS for episodes / podcasts.
     Music tracks + audiobook chapters skip this and chain silently at EOS
     so the outro plays through. Play now / Cancel are buttons: ←/→ choose,
     OK presses, the pointer clicks them; Back cancels. Outside .player
     (a fixed box: its own stacking context) so it also shows over the
     now-playing view of a podcast's audio episode. Which puts it over the
     pickers too (inside .player), in the same top-right corner: it steps
     aside while one is open (upNextCovered), and comes back as it was. -->
{#if upNextShown && nextSibling && !loading && !error && !refused && !upNextCovered()}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="up-next" onmousemove={onPointerMove}>
    <div class="up-next-label">UP NEXT · {upNextCountdown}s</div>
    <div class="up-next-title">{nextSibling.title}</div>
    <div class="up-next-buttons">
      <button
        type="button"
        tabindex="-1"
        class="up-next-button"
        class:focused={upNextFocus === 0}
        onclick={(e) => clickUpNext('play', e)}
        onmouseenter={() => hoverUpNext(0)}
        onmouseleave={leaveHover}
      >▶ Play now</button>
      <button
        type="button"
        tabindex="-1"
        class="up-next-button"
        class:focused={upNextFocus === 1}
        onclick={(e) => clickUpNext('cancel', e)}
        onmouseenter={() => hoverUpNext(1)}
        onmouseleave={leaveHover}
      >Cancel</button>
    </div>
    {#if upNextFocus < 0}<div class="up-next-hint">↑ choose · back cancel</div>{/if}
  </div>
{/if}

<!-- Rebuffering (a seek, a thin buffer, a session re-issue). Outside
     .player so it also shows over the music / audiobook view, which sits
     above the player. -->
{#if buffering && !loading && !error && !refused}
  <div class="buffering"><Spinner label="" /></div>
{/if}

<style>
  /* webOS 6 runs Chromium 79: no `inset` (87) and no flexbox `gap` (84) in
     this file. Edges are spelled out and flex spacing is margins; grid gap
     (66) is fine. */
  .player {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    left: 0;
    background: #000;
    overflow: hidden;
  }

  .video {
    width: 100%;
    height: 100%;
    object-fit: contain;
  }

  .video.hidden {
    display: none;
  }

  /* ── Music / audiobook view ────────────────────────────────── */
  .music-view {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 100;
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
    line-height: 1.2;
    margin-top: 28px;
  }
  .music-subtitle {
    display: flex;
    align-items: baseline;
    justify-content: center;
    flex-wrap: wrap;
    font-size: var(--font-lg);
    color: var(--text-secondary);
    text-align: center;
    margin-top: 20px;
  }
  .music-artist { color: var(--text-primary); font-weight: 500; }
  .music-album { font-style: italic; }
  .music-dot { color: var(--text-muted); margin: 0 14px; }

  .music-meta {
    display: flex;
    font-size: var(--font-md);
    color: var(--text-secondary);
    margin-top: 28px;
  }
  .music-meta > span + span { margin-left: 24px; }
  .music-bar {
    display: grid;
    grid-template-columns: 120px 1fr 120px;
    align-items: center;
    gap: 28px;
    width: 100%;
    margin-top: 28px;
    color: var(--text-primary);
    font-size: var(--font-md);
    font-variant-numeric: tabular-nums;
  }
  .music-elapsed { text-align: right; color: var(--text-secondary); }
  .music-remaining { text-align: left; color: var(--text-secondary); }
  /* A pending scrub target (not yet sought to): the time reads in the
     accent colour so it's clearly "where OK will go", not the playhead. */
  .music-elapsed.scrubbing,
  .elapsed.scrubbing {
    color: var(--accent);
    font-weight: 600;
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
    font-size: var(--font-sm);
    color: var(--text-muted);
    margin-top: 36px;
  }
  .music-hints > span + span { margin-left: 32px; }
  .music-speed {
    color: var(--text-primary);
  }
  .music-note {
    font-size: var(--font-sm);
    color: var(--text-secondary);
    margin-top: 16px;
  }
  .picker.speed-picker {
    min-width: 240px;
  }

  .overlay {
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    left: 0;
  }

  .overlay.center {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    background: rgba(0, 0, 0, 0.7);
  }
  .overlay.center > div + div { margin-top: 20px; }
  .overlay .title {
    max-width: 80%;
    text-align: center;
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
    top: 0;
    right: 0;
    bottom: 0;
    left: 0;
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
  /* An episode's "Show · S2 · E5", over its title. */
  .now-playing-context {
    font-size: var(--font-md);
    color: rgba(255, 255, 255, 0.75);
    margin-bottom: 8px;
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
    font-size: var(--font-sm);
    color: rgba(255, 255, 255, 0.6);
  }

  /* On-screen actions under the bar. .controls passes the pointer through
     (pointer-events: none) and so does the row, which runs the controls'
     width over Skip Intro (bottom right): its buttons take it back for the
     Magic Remote. */
  .actions {
    display: flex;
    margin-top: 24px;
    pointer-events: none;
  }
  .action {
    display: inline-block;
    padding: 10px 24px;
    border: 0;
    border-radius: 26px;
    background: rgba(255, 255, 255, 0.14);
    color: white;
    font-family: inherit;
    font-size: var(--font-sm);
    line-height: 1.3;
    cursor: pointer;
    pointer-events: auto;
  }
  .action + .action { margin-left: 16px; }
  .action.focused {
    background: var(--accent);
    box-shadow:
      0 0 0 4px var(--focus-ring),
      0 0 24px 6px rgba(124, 106, 247, 0.5);
  }
  /* The colour key that opens the same picker. */
  .key-dot {
    display: inline-block;
    width: 14px;
    height: 14px;
    margin-right: 10px;
    border-radius: 50%;
    vertical-align: middle;
    position: relative;
    top: -2px;
  }
  .key-yellow { background: #f5c518; }
  .key-blue { background: #3b82f6; }

  /* The pointer's playback buttons, only while the pointer is on screen
     (centred on the now-playing view, under the bar on video). Like the
     action row's, the buttons take the pointer back from .controls and the
     row's empty width doesn't; :hover is their selection effect (the
     pointer is what reaches them). */
  .transport {
    display: flex;
    justify-content: center;
    margin-top: 28px;
    pointer-events: none;
  }
  .bottom .transport {
    justify-content: flex-start;
    margin-top: 24px;
  }
  .transport-button {
    display: inline-block;
    min-width: 104px;
    padding: 10px 24px;
    border: 0;
    border-radius: 26px;
    background: rgba(255, 255, 255, 0.14);
    color: white;
    font-family: inherit;
    font-size: var(--font-sm);
    line-height: 1.3;
    text-align: center;
    cursor: pointer;
    pointer-events: auto;
  }
  .transport-button + .transport-button { margin-left: 16px; }
  .transport-button:hover,
  .transport-button.active {
    background: var(--accent);
    box-shadow:
      0 0 0 4px var(--focus-ring),
      0 0 24px 6px rgba(124, 106, 247, 0.5);
  }
  /* The bar under the pointer: taller, and it takes clicks (seek there). */
  .track.clickable,
  .music-track.clickable {
    pointer-events: auto;
    cursor: pointer;
  }
  .track.clickable:hover { height: 14px; }
  .music-track.clickable:hover { height: 16px; }

  /* Subtitles: outlined white text near the bottom, raised above the bar
     while the controls show. Lines are blocks (no flexbox gap). */
  .subtitle-overlay {
    position: absolute;
    left: 6%;
    right: 6%;
    bottom: 8%;
    text-align: center;
    pointer-events: none;
  }
  .subtitle-overlay.raised {
    bottom: 330px;
  }
  .subtitle-cue + .subtitle-cue,
  .subtitle-cue + .subtitle-notice {
    margin-top: 10px;
  }
  .subtitle-line {
    display: block;
    font-size: 46px;
    font-weight: 600;
    line-height: 1.3;
    color: #fff;
    text-shadow:
      -2px -2px 0 #000,
      2px -2px 0 #000,
      -2px 2px 0 #000,
      2px 2px 0 #000,
      0 0 8px rgba(0, 0, 0, 0.9);
  }
  .subtitle-notice {
    display: inline-block;
    padding: 8px 20px;
    border-radius: 8px;
    background: rgba(7, 7, 13, 0.8);
    color: var(--text-secondary);
    font-size: var(--font-md);
  }

  /* A button for the pointer's click; the look of the old label. Over the
     controls, which come after it (their rows used to take its clicks),
     under a picker (z-index 5). */
  .skip-marker {
    position: absolute;
    z-index: 4;
    bottom: 80px;
    right: 60px;
    padding: 14px 26px;
    border: 0;
    background: var(--accent);
    color: #fff;
    font-family: inherit;
    font-size: var(--font-md);
    font-weight: 600;
    line-height: inherit;
    border-radius: 24px;
    cursor: pointer;
  }
  .skip-marker:hover {
    box-shadow:
      0 0 0 4px #fff,
      0 0 24px 6px rgba(124, 106, 247, 0.5);
  }

  .picker {
    position: absolute;
    top: 80px;
    right: 60px;
    /* Over the controls' gradient, which stays up while a picker is open. */
    z-index: 5;
    padding: 24px 32px;
    background: rgba(7, 7, 13, 0.92);
    border: 2px solid var(--border-strong);
    border-radius: 12px;
    min-width: 360px;
    max-width: 520px;
  }
  .picker.picker-wide {
    max-width: 760px;
  }
  /* Rows are buttons so the pointer can click them; drop the button look
     (before .picker-row, which sets the text). */
  .picker-button {
    display: block;
    width: 100%;
    margin: 0;
    border: 0;
    background: none;
    font-family: inherit;
    line-height: inherit;
    text-align: left;
    cursor: pointer;
  }
  /* More rows above / below the window drawn. */
  .picker-more {
    font-size: var(--font-xs);
    color: var(--text-muted);
    text-align: center;
    line-height: 1;
    padding: 4px 0;
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
    align-items: baseline;
  }
  .online-sub-lang {
    margin-right: 12px;
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

  /* Fixed, over the picture and the now-playing view (z-index 100), under
     the rebuffer spinner (110). */
  .up-next {
    position: fixed;
    top: 60px;
    right: 60px;
    z-index: 105;
    padding: 18px 28px;
    background: rgba(7, 7, 13, 0.85);
    border: 2px solid var(--accent);
    border-radius: 12px;
    max-width: 420px;
  }
  /* "UP NEXT" is upper case in the markup: a text-transform here also
     upper-cased the countdown's unit ("10S"). */
  .up-next-label {
    font-size: var(--font-sm);
    color: var(--accent);
    letter-spacing: 0.15em;
    margin-bottom: 6px;
  }
  .up-next-title {
    font-size: var(--font-md);
    color: var(--text-primary);
    margin-bottom: 10px;
  }
  .up-next-hint {
    margin-top: 12px;
    font-size: var(--font-sm);
    color: var(--text-secondary);
  }
  .up-next-buttons {
    white-space: nowrap;
  }
  .up-next-button {
    display: inline-block;
    padding: 10px 24px;
    border: 0;
    border-radius: 26px;
    background: rgba(255, 255, 255, 0.14);
    color: white;
    font-family: inherit;
    font-size: var(--font-sm);
    line-height: 1.3;
    cursor: pointer;
  }
  .up-next-button + .up-next-button { margin-left: 14px; }
  .up-next-button.focused {
    background: var(--accent);
    box-shadow:
      0 0 0 4px var(--focus-ring),
      0 0 24px 6px rgba(124, 106, 247, 0.5);
  }

  .hints > span + span { margin-left: 32px; }
  .online-sub-meta > span + span { margin-left: 12px; }

  /* Centred over everything, the music view included (z-index 100). */
  .buffering {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    z-index: 110;
    pointer-events: none;
  }
</style>
