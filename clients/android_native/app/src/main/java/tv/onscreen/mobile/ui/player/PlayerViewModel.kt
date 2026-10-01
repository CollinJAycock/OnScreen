package tv.onscreen.mobile.ui.player

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.job
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import retrofit2.HttpException
import tv.onscreen.mobile.data.api.HeartbeatRefusal
import tv.onscreen.mobile.data.api.apiError
import tv.onscreen.mobile.data.artworkUrl
import tv.onscreen.mobile.data.model.AudioStream
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.PlaybackStop
import tv.onscreen.mobile.data.model.Marker
import tv.onscreen.mobile.data.model.SubtitleStream
import tv.onscreen.mobile.data.model.TranscodeSession
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.prefs.SubtitlePrefs
import tv.onscreen.mobile.data.prefs.SubtitleStyle
import tv.onscreen.mobile.data.repository.AudiobookRepository
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.model.OnlineSubtitle
import tv.onscreen.mobile.data.repository.NotificationsRepository
import tv.onscreen.mobile.data.repository.OnlineSubtitleRepository
import tv.onscreen.mobile.data.repository.PreferencesRepository
import tv.onscreen.mobile.data.repository.ServerCapabilitiesRepository
import tv.onscreen.mobile.data.repository.TranscodeRepository
import tv.onscreen.mobile.data.repository.TrickplayRepository
import tv.onscreen.mobile.data.repository.WatchLimitRepository
import androidx.media3.common.util.UnstableApi
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.isActive
import tv.onscreen.mobile.playback.AudiobookSpeed
import tv.onscreen.mobile.playback.LocalProgressTracker
import tv.onscreen.mobile.playback.MusicQueue
import tv.onscreen.mobile.playback.NextSiblingResolver
import tv.onscreen.mobile.playback.PlaybackService
import tv.onscreen.mobile.playback.StopAfterItem
import tv.onscreen.mobile.playback.StreamTokenVault
import tv.onscreen.mobile.trickplay.TrickplayVtt
import javax.inject.Inject

sealed class PlaybackSource {
    data class DirectPlay(val url: String, val startMs: Long) : PlaybackSource()

    /** A remux / transcode session. [offsetMs]: the content time the stream
     *  opens at — its 0:00. [requestedMs]: the content time playback was
     *  asked to start at. */
    data class Hls(val playlistUrl: String, val offsetMs: Long, val requestedMs: Long) : PlaybackSource() {
        /** Where in the STREAM the player starts, so playback begins at
         *  [requestedMs] (web's desiredStartSec). A session that opens
         *  mid-file opens at or just before the request — a remux copies the
         *  video, so only at a keyframe, up to several seconds earlier. A
         *  full-timeline one (the ABR and pre-encoded ladders report a
         *  start_offset_sec of 0) opens at 0:00 of the file, and resuming
         *  means seeking in it: starting it at 0 played a 45:00 resume from
         *  the top, and the first progress beat saved that over the resume
         *  point. */
        val startMs: Long get() = (requestedMs - offsetMs).coerceAtLeast(0L)
    }
}

/** Snapshot of the OpenSubtitles search dialog. Kept on the VM so
 *  the dialog survives configuration changes (orientation flips
 *  during search) without dropping in-flight results. */
data class OnlineSubtitleSearchUi(
    val loading: Boolean = false,
    val results: List<OnlineSubtitle> = emptyList(),
    val error: String? = null,
)

data class PlayerUiState(
    val loading: Boolean = true,
    val source: PlaybackSource? = null,
    val item: ItemDetail? = null,
    val audioStreams: List<AudioStream> = emptyList(),
    /** Remux / transcode: the audio stream the server session carries, as its
     *  position in [audioStreams] — the server's audio-relative
     *  audio_stream_index (see AudioSelection). Null on direct play, where the
     *  player's own track selection says which. */
    val sessionAudioRow: Int? = null,
    /** The row an audio switch is moving to, while its replacement session
     *  starts — seconds on a cold ffmpeg start, during which
     *  [sessionAudioRow] still names the old one. Null when no switch is
     *  under way: it clears once the switch lands or fails. */
    val pendingAudioRow: Int? = null,
    val subtitles: List<SubtitleStream> = emptyList(),
    /** Every subtitle the playing file has — its embedded streams, then the
     *  files attached to it on the server — with the side-load urls a
     *  remux / transcode needs (see SubtitleTracks). */
    val subtitleTracks: List<SubtitleTrack> = emptyList(),
    val markers: List<Marker> = emptyList(),
    val nextSibling: ChildItem? = null,
    val preferredAudioLang: String? = null,
    val preferredSubtitleLang: String? = null,
    /** When true (and a preferred subtitle language is set), auto-
     *  selection enables only a FORCED subtitle track in that language;
     *  if none exists, subtitles stay off. Mirrors the web client's
     *  forcedOnly arg to pickPreferredSubtitle. */
    val forcedSubtitlesOnly: Boolean = false,
    /** Trickplay cues for this item's seekbar previews. Null while
     *  loading or if the item has no generated trickplay (e.g. a fresh
     *  scan that hasn't run the trickplay task yet). */
    val trickplayCues: List<tv.onscreen.mobile.data.model.TrickplayCue>? = null,
    /** Parsed LRC cues for the music-player overlay. Null = no
     *  lyrics available (non-track item, or server returned the
     *  empty-cache sentinel). */
    val lyricsCues: List<tv.onscreen.mobile.lyrics.LrcParser.Cue>? = null,
    /** Plain-text fallback when [lyricsCues] is empty but the server
     *  has unstamped lyrics. Renderer drops to a static scroll. */
    val lyricsPlain: String? = null,
    /** Currently-displayed scrub-preview bitmap. Set while the user is
     *  dragging the seekbar; cleared on scrub-end. */
    val scrubPreview: tv.onscreen.mobile.ui.player.ScrubPreview? = null,
    /** Audiobooks: the book's listening speed once known (null for anything
     *  else, and until the lookup returns). The screen applies it to a
     *  screen-owned player; PlaybackService applies it to its own. */
    val listeningRate: Float? = null,
    /** Audiobooks: whether the server takes bookmarks (false until the
     *  speed lookup says so, and on a server that predates them). */
    val bookmarksSupported: Boolean = false,
    /** The background PlaybackService already has this item current: the
     *  screen binds to it as it is, and never hands it over again. Nothing
     *  new starts streaming, so the cellular prompt doesn't apply either. */
    val playingInService: Boolean = false,
    /** Audio: the cover the player shows, as a full URL. The item's own
     *  poster, else its parent's: the scanner puts the art on the album,
     *  the book or the podcast, and a track or chapter seldom has one of
     *  its own. Null until resolved, and when neither has one. */
    val artworkUrl: String? = null,
    /** The cover lookup has finished: without [artworkUrl] now, there is
     *  none to show and the player shows its placeholder. */
    val artworkChecked: Boolean = false,
    val error: String? = null,
) {
    /** Remux / transcode: the audio row the viewer has, or is getting — what
     *  the picker marks, and what a pick is "already playing" against. */
    val targetAudioRow: Int? get() = pendingAudioRow ?: sessionAudioRow
}

/** Outcome of "Add bookmark", for the screen's confirmation toast. */
sealed class BookmarkNotice {
    data class Added(val positionMs: Long) : BookmarkNotice()
    /** 409 BOOKMARK_LIMIT: the book already has the most allowed. */
    data object LimitReached : BookmarkNotice()
    /** 404: the server predates bookmarks (the action is hidden after). */
    data object Unsupported : BookmarkNotice()
    data object Failed : BookmarkNotice()
}

/** What the sleep timer needs from the player the screen has bound: where
 *  it is in CONTENT time (chapter marks are content-absolute), how fast it
 *  is playing, and how to pause it. */
class SleepTimerHooks(
    val positionMs: () -> Long,
    val speed: () -> Float,
    val pause: () -> Unit,
)

/** Snapshot the player overlay reads to render the thumbnail above
 *  the seekbar. positionMs is what the user is scrubbing toward;
 *  bitmap is the cropped sprite for the cue covering that position
 *  (null when we haven't fetched it yet — caller shows a spinner). */
data class ScrubPreview(
    val positionMs: Long,
    val bitmap: android.graphics.Bitmap?,
)

@HiltViewModel
class PlayerViewModel @Inject constructor(
    private val itemRepo: ItemRepository,
    private val transcodeRepo: TranscodeRepository,
    private val preferencesRepo: PreferencesRepository,
    private val serverPrefs: ServerPrefs,
    private val subtitlePrefs: SubtitlePrefs,
    private val playbackPrefs: tv.onscreen.mobile.data.prefs.PlaybackPrefs,
    private val downloads: tv.onscreen.mobile.data.downloads.OnScreenDownloadManager,
    private val notifications: NotificationsRepository,
    private val onlineSubtitles: OnlineSubtitleRepository,
    private val trickplayRepo: TrickplayRepository,
    private val watchLimitRepo: WatchLimitRepository,
    private val audiobooks: AudiobookRepository,
    private val serverCapabilities: ServerCapabilitiesRepository,
) : ViewModel() {

    /** Whether to gate video playback behind a "you're on cellular,
     *  continue?" confirmation. Reads the user pref; PlayerScreen
     *  pairs this with a connectivity check before deciding. */
    suspend fun shouldWarnCellular(): Boolean = playbackPrefs.getWarnOnCellularStream()

    /** Per-session sprite-sheet cache. Trickplay sheets are typically
     *  10x10 grids of small JPGs, so the whole movie's worth is just a
     *  handful of bitmaps — the bytes don't justify an LRU eviction
     *  policy, just hold them for the session and let the VM clear on
     *  scope death. Keyed `(itemId, file)` so two players in quick
     *  succession on different items don't reuse stale sprites. */
    private val spriteCache = mutableMapOf<Pair<String, String>, android.graphics.Bitmap>()
    private var currentTrickplayItemId: String? = null

    private val _state = MutableStateFlow(PlayerUiState())
    val state: StateFlow<PlayerUiState> = _state.asStateFlow()

    /** Reactive subtitle styling preferences. UI binds to this — when
     *  it changes the player applies the new style to its SubtitleView
     *  immediately (see [tv.onscreen.mobile.ui.player.applySubtitleStyle]). */
    val subtitleStyle: Flow<SubtitleStyle> = subtitlePrefs.style

    fun setSubtitleSize(s: SubtitleStyle.Size) {
        viewModelScope.launch { subtitlePrefs.setSize(s) }
    }

    fun setSubtitleColor(c: SubtitleStyle.TextColor) {
        viewModelScope.launch { subtitlePrefs.setColor(c) }
    }

    fun setSubtitleBackground(b: SubtitleStyle.Background) {
        viewModelScope.launch { subtitlePrefs.setBackground(b) }
    }

    fun setSubtitleOutline(o: SubtitleStyle.Outline) {
        viewModelScope.launch { subtitlePrefs.setOutline(o) }
    }

    // ── Trickplay (seekbar thumbnails) ────────────────────────────────

    /** Fetches the trickplay status + cue list for the active item.
     *  Best-effort: any failure leaves [PlayerUiState.trickplayCues]
     *  null, which the player overlay reads as "no thumbnails — show
     *  position-only scrub." */
    private fun loadTrickplayCues(itemId: String) {
        viewModelScope.launch {
            currentTrickplayItemId = itemId
            // Recycle old sheets explicitly — Bitmap.clear-the-map only
            // releases the Java references, the native pixel memory
            // sticks around until GC runs. For a 90-min film with five
            // 10x10 sprite sheets that's a couple of MB held against
            // the heap pressure budget. Cropped previews already
            // carry their own pixel copy, so it's safe to recycle the
            // source sheet without invalidating the live preview.
            recycleSpriteCache()
            val status = trickplayRepo.status(itemId)
            if (status.status != "done") return@launch
            val cues = trickplayRepo.fetchCues(itemId) ?: return@launch
            // Keep ordering stable; consumer uses TrickplayVtt.cueAt
            // which assumes ascending start times.
            _state.value = _state.value.copy(trickplayCues = cues)
        }
    }

    /** Called from the player overlay's TimeBar OnScrubListener as the
     *  user drags. Looks up the cue covering [positionMs], fetches /
     *  caches its sprite, and emits a [ScrubPreview] so the overlay
     *  re-renders with the new bitmap. The bitmap field is null
     *  briefly while a sprite is downloading; the overlay shows a
     *  position-only label in that window. */
    fun onScrubMove(positionMs: Long) {
        val cues = _state.value.trickplayCues
        if (cues.isNullOrEmpty()) {
            _state.value = _state.value.copy(scrubPreview = ScrubPreview(positionMs, null))
            return
        }
        val cue = TrickplayVtt.cueAt(cues, positionMs)
            ?: TrickplayVtt.cueAt(cues, cues.last().endMs - 1) // clamp past EOF
        if (cue == null) {
            _state.value = _state.value.copy(scrubPreview = ScrubPreview(positionMs, null))
            return
        }
        val itemId = currentTrickplayItemId ?: return
        val key = itemId to cue.file
        val cached = spriteCache[key]
        // Always set the preview synchronously so the overlay knows the
        // current scrub position even before the sprite arrives.
        _state.value = _state.value.copy(
            scrubPreview = ScrubPreview(positionMs, cropped(cached, cue)),
        )
        if (cached == null) {
            viewModelScope.launch {
                val sheet = trickplayRepo.fetchSprite(itemId, cue.file) ?: return@launch
                spriteCache[key] = sheet
                // If the user is still scrubbing on a position covered
                // by this cue, refresh the preview with the cropped
                // bitmap. If they've moved on, the next onScrubMove
                // will overwrite anyway — no race-cleanup needed.
                val current = _state.value.scrubPreview ?: return@launch
                if (TrickplayVtt.cueAt(cues, current.positionMs) == cue) {
                    _state.value = _state.value.copy(
                        scrubPreview = ScrubPreview(current.positionMs, cropped(sheet, cue)),
                    )
                }
            }
        }
    }

    /** Clears the scrub-preview state so the overlay disappears. Called
     *  when the user releases the seekbar. Doesn't clear cues — those
     *  are valid for the rest of the session. */
    fun onScrubStop() {
        _state.value = _state.value.copy(scrubPreview = null)
    }

    // ── Artwork ───────────────────────────────────────────────────────

    /** Resolve the audio player's cover ([PlayerUiState.artworkUrl]).
     *  Best-effort, and never fails playback: with no poster anywhere the
     *  player shows its placeholder. */
    private fun loadArtwork(item: ItemDetail) {
        if (item.type !in AUDIO_TYPES) return
        viewModelScope.launch {
            val url = orNull { artworkUrlFor(item) }
            // Another prepare() took over while this loaded.
            if (_state.value.item?.id != item.id) return@launch
            _state.update { it.copy(artworkUrl = url ?: it.artworkUrl, artworkChecked = true) }
        }
    }

    /**
     * The cover of [itemId], which the background service is playing, from
     * its queue entry: its [type] and the [parentId] the service recorded.
     * No network, and nothing unless the parent is in the recent-items cache
     * — then the cover shows before the item itself loads. [loadArtwork]
     * still settles it once the item is here.
     */
    fun primeArtwork(itemId: String, type: String?, parentId: String?) {
        if (preparedItemId != itemId || _state.value.artworkUrl != null) return
        if (type !in PARENT_ART_TYPES || parentId == null) return
        val path = itemRepo.cachedItem(parentId)?.poster_path ?: return
        val server = lastServerOrigin?.trimEnd('/')?.takeIf { it.isNotEmpty() } ?: return
        _state.update { it.copy(artworkUrl = artworkUrl(server, path, width = ARTWORK_WIDTH)) }
    }

    /** The item's poster, else — for a track, a chapter or an episode — its
     *  album's, book's or podcast's. Not a book's parent: that is its
     *  author, whose portrait is no cover. The parent usually comes from the
     *  recent-items cache: the page the player was opened from fetched it. */
    private suspend fun artworkUrlFor(item: ItemDetail): String? {
        val parentId = item.parent_id?.takeIf { item.type in PARENT_ART_TYPES }
        val path = item.poster_path ?: parentId?.let { id ->
            (itemRepo.cachedItem(id) ?: orNull { itemRepo.getItem(id) })?.poster_path
        } ?: return null
        val server = serverPrefs.getServerUrl()?.trimEnd('/')?.takeIf { it.isNotEmpty() } ?: return null
        return artworkUrl(server, path, width = ARTWORK_WIDTH)
    }

    // ── Lyrics ────────────────────────────────────────────────────────

    /** Fetch + parse lyrics for the active track. Called from the
     *  prepare flow on track items only. Failure (HTTP error, parse
     *  error, no lyrics in the cache + LRCLIB) leaves both fields
     *  null and the overlay hides. */
    private fun loadLyrics(itemId: String) {
        viewModelScope.launch {
            val resp = try { itemRepo.getLyrics(itemId) } catch (_: Exception) { null }
            if (resp == null) return@launch
            val cues = if (resp.synced.isNotEmpty()) {
                tv.onscreen.mobile.lyrics.LrcParser.parse(resp.synced)
            } else {
                emptyList()
            }
            _state.value = _state.value.copy(
                lyricsCues = cues.takeIf { it.isNotEmpty() },
                lyricsPlain = resp.plain.takeIf { it.isNotEmpty() },
            )
        }
    }

    /** Crop a sprite-sheet to the cue's xywh region. Returns the same
     *  bitmap unchanged when the cue covers the entire sheet (single-
     *  sprite videos), otherwise allocates a sub-bitmap. Null in
     *  means "sprite not loaded yet" — propagate as null. */
    private fun cropped(sheet: android.graphics.Bitmap?, cue: tv.onscreen.mobile.data.model.TrickplayCue): android.graphics.Bitmap? {
        if (sheet == null) return null
        val sw = sheet.width
        val sh = sheet.height
        // Defensive clamp — if the server emits coords that overshoot
        // the actual sprite, BitmapCreate would throw.
        val x = cue.x.coerceIn(0, sw - 1)
        val y = cue.y.coerceIn(0, sh - 1)
        val w = cue.w.coerceIn(1, sw - x)
        val h = cue.h.coerceIn(1, sh - y)
        if (x == 0 && y == 0 && w == sw && h == sh) return sheet
        return android.graphics.Bitmap.createBitmap(sheet, x, y, w, h)
    }

    // ── Sleep timer ───────────────────────────────────────────────────

    /** Active sleep-timer countdown; null = no timer running. The UI
     *  shows a chip + remaining time when this is non-null and fires
     *  the pause-player action when it reaches 0 (or, for EndOfTrack
     *  mode, when the player itself emits ENDED). */
    private val _sleepTimer = MutableStateFlow<SleepTimerState?>(null)
    val sleepTimer: StateFlow<SleepTimerState?> = _sleepTimer.asStateFlow()

    /** Edge-triggered "the timer expired — pause now" signal. UI
     *  collects this from a side-effect coroutine and calls
     *  player.pause(); we don't reach into the ExoPlayer instance from
     *  the VM. Cleared back to null after consumed. */
    private val _sleepTimerFired = MutableStateFlow(false)
    val sleepTimerFired: StateFlow<Boolean> = _sleepTimerFired.asStateFlow()

    private var sleepTimerJob: kotlinx.coroutines.Job? = null

    /** Monotonic clock for handing a countdown to the next screen
     *  ([handOffSleepTimer]). A seam for tests. */
    internal var elapsedRealtime: () -> Long = { android.os.SystemClock.elapsedRealtime() }

    /** Start a wall-clock countdown sleep timer. Replaces any running
     *  timer. Off cancels. EndOfTrack / EndOfChapter switch to the
     *  content-aware modes (no countdown — the UI shows the label and we
     *  pause when the player emits Player.STATE_ENDED, or for EndOfChapter
     *  at the next embedded chapter mark, whichever comes first). */
    fun setSleepTimer(mode: SleepTimer) {
        sleepTimerJob?.cancel()
        sleepTimerJob = null
        _sleepTimerFired.value = false
        disarmStopAfterItem()
        when (mode) {
            SleepTimer.Off -> {
                _sleepTimer.value = null
            }
            SleepTimer.EndOfTrack, SleepTimer.EndOfChapter -> {
                // No countdown; stash the mode so the player's
                // onPlaybackStateChanged listener can fire pause when it
                // sees STATE_ENDED — and tell the background service to
                // end there rather than chain on (see StopAfterItem).
                _sleepTimer.value = SleepTimerState(mode = mode, remainingMs = 0)
                // The prepared id stands in while the item is still loading
                // (a screen bound to the service's item shows before it has).
                (_state.value.item?.id ?: preparedItemId)?.let(::armStopAfterItem)
                if (mode == SleepTimer.EndOfChapter) startChapterWatch()
            }
            is SleepTimer.Minutes -> startCountdown(mode, SleepTimerMath.initialMs(mode))
        }
    }

    private fun startCountdown(mode: SleepTimer.Minutes, remainingMs: Long) {
        _sleepTimer.value = SleepTimerState(mode = mode, remainingMs = remainingMs)
        sleepTimerJob = viewModelScope.launch {
            var remaining = remainingMs
            while (remaining > 0) {
                delay(1_000)
                remaining -= 1_000
                _sleepTimer.value = _sleepTimer.value?.copy(remainingMs = remaining)
            }
            firePause()
        }
    }

    /** Called by the screen's playback-state listener when the player
     *  emits STATE_ENDED. If the active timer waits for the item to end
     *  (EndOfTrack, or EndOfChapter — a multi-file book's chapter IS the
     *  item), fire pause; otherwise no-op (a track ending under a Minutes
     *  timer doesn't pause early — auto-advance handles next-track). */
    fun onPlayerEnded() {
        if (SleepTimerMath.endsWithItem(_sleepTimer.value?.mode)) {
            firePause()
        }
    }

    /** Whether the running timer stops at the end of the current item. */
    fun sleepsAtItemEnd(): Boolean = SleepTimerMath.endsWithItem(_sleepTimer.value?.mode)

    /** Acknowledge the fired signal so it can edge-trigger again. */
    fun consumeSleepTimerFired() {
        _sleepTimerFired.value = false
    }

    /** The player the screen has bound, while one is.
     *
     *  The fired edge used to be delivered ONLY through a state flow the
     *  screen collected with collectAsStateWithLifecycle — which stops
     *  collecting below STARTED. So with the screen off, the timer counted
     *  down, raised the edge, and nothing ever consumed it: playback ran on.
     *  That is the timer's whole purpose (fall asleep to a show), so the
     *  primary path was the broken one. Invoking a registered action from
     *  the VM's own coroutine works regardless of UI lifecycle state — and
     *  the end-of-chapter watch reads the position the same way. */
    private var sleepHooks: SleepTimerHooks? = null

    fun setSleepTimerHooks(hooks: SleepTimerHooks?) {
        sleepHooks = hooks
        // An end-of-chapter timer set (or handed over) before a player was
        // bound starts watching now.
        if (hooks != null && _sleepTimer.value?.mode == SleepTimer.EndOfChapter &&
            sleepTimerJob?.isActive != true
        ) {
            startChapterWatch()
        }
    }

    /** Where the end-of-chapter timer stops, in content time; null = at the
     *  end of the item (STATE_ENDED). */
    private var chapterStopMs: Long? = null

    /**
     * End of chapter in a single-file book: watch the position and pause at
     * the next embedded chapter mark ([SleepTimerMath.chapterEndMs]). Wakes
     * just in time for the mark (the wait scales with the speed), at most a
     * second apart so a seek is caught. A file without chapter marks — and
     * every multi-file chapter — has nothing to watch: [onPlayerEnded] stops
     * it at the item's end.
     */
    private fun startChapterWatch() {
        val chapters = _state.value.item?.files?.firstOrNull()?.chapters.orEmpty()
        val hooks = sleepHooks ?: return
        if (chapters.isEmpty()) return
        sleepTimerJob?.cancel()
        chapterStopMs = SleepTimerMath.chapterEndMs(chapters, hooks.positionMs())
        sleepTimerJob = viewModelScope.launch {
            while (isActive && _sleepTimer.value?.mode == SleepTimer.EndOfChapter) {
                val bound = sleepHooks ?: break
                val pos = bound.positionMs()
                val target = chapterStopMs
                if (target != null && pos >= target) {
                    firePause()
                    break
                }
                delay(SleepTimerMath.nextCheckDelayMs(target, pos, bound.speed()))
            }
        }
    }

    /** The listener seeked (chapter picker, scrubbing, skip buttons): an
     *  end-of-chapter timer now means the end of the chapter landed in. */
    fun onSleepTimerSeek(positionMs: Long) {
        if (_sleepTimer.value?.mode != SleepTimer.EndOfChapter) return
        val chapters = _state.value.item?.files?.firstOrNull()?.chapters.orEmpty()
        if (chapters.isNotEmpty()) chapterStopMs = SleepTimerMath.chapterEndMs(chapters, positionMs)
    }

    /**
     * The screen is about to follow the background queue onto [nextItemId]
     * (a chapter chained, a lock-screen skip): hand the running timer to the
     * screen that opens for it. Without this the timer died with this
     * ViewModel at every chapter of a multi-file book.
     */
    fun handOffSleepTimer(nextItemId: String) {
        val st = _sleepTimer.value ?: return
        SleepTimerCarry.put(nextItemId, st.mode, st.remainingMs, elapsedRealtime())
    }

    /** Take over a timer the previous screen handed to [itemId]. */
    private fun adoptSleepTimer(itemId: String) {
        val st = SleepTimerCarry.take(itemId, elapsedRealtime()) ?: return
        sleepTimerJob?.cancel()
        _sleepTimerFired.value = false
        when (val mode = st.mode) {
            is SleepTimer.Minutes -> if (st.remainingMs > 0) startCountdown(mode, st.remainingMs) else firePause()
            SleepTimer.EndOfTrack, SleepTimer.EndOfChapter -> {
                _sleepTimer.value = st
                armStopAfterItem(itemId)
                // The chapter watch starts once the item (its chapter marks)
                // has loaded — see loadItemExtras().
            }
            SleepTimer.Off -> Unit
        }
    }

    /** Item the service was asked to stop after (see [StopAfterItem]). */
    private var stopArmedFor: String? = null

    private fun armStopAfterItem(itemId: String) {
        stopArmedFor = itemId
        StopAfterItem.arm(itemId)
    }

    private fun disarmStopAfterItem() {
        stopArmedFor?.let(StopAfterItem::disarm)
        stopArmedFor = null
    }

    /** Fire the pause directly AND raise the edge for an attached screen. */
    private fun firePause() {
        _sleepTimerFired.value = true
        _sleepTimer.value = null
        disarmStopAfterItem()
        sleepHooks?.pause?.invoke()
    }

    /** Server origin used to build absolute URLs that Cast receivers
     *  can fetch from the LAN. Returns null when not yet logged in /
     *  before the auth flow completes. Pulled synchronously off the
     *  Flow's last value via runBlocking { first() } would block; we
     *  instead surface it as a one-shot snapshot the UI grabs at click
     *  time. The server URL doesn't change during a session, so caching
     *  the latest emit is safe. */
    private var lastServerOrigin: String? = null
    init {
        viewModelScope.launch {
            serverPrefs.serverUrl.collect { lastServerOrigin = it }
        }
    }
    fun serverOrigin(): String? = lastServerOrigin

    /** Cross-device resume signal. Emits a seek-to position (ms)
     *  whenever the server reports new progress for the currently-
     *  loaded item from another device. The screen consumes this and
     *  seeks the ExoPlayer instance — same-device echoes are dropped
     *  by ID below so we don't fight ourselves. */
    private val _remoteResumeMs = MutableStateFlow<Long?>(null)
    val remoteResumeMs: StateFlow<Long?> = _remoteResumeMs.asStateFlow()

    private var sseJob: kotlinx.coroutines.Job? = null
    private var localProgressMs: Long = 0L

    /** The server keeps the duration it knows when a progress report leaves
     *  it out (see ServerFeatures.progress_without_duration). Asked when
     *  [prepare] starts; false until the answer is in, and when there is
     *  none — reports with no duration are then skipped, as they always were. */
    private var progressWithoutDuration = false

    private var transcodeSessionId: String? = null

    /** Id of the item the background PlaybackService has current, or null.
     *  A seam so tests can stand in for the service. */
    @OptIn(UnstableApi::class)
    internal var backgroundItemId: () -> String? = { PlaybackService.currentItemId }

    /** The playable item prepare() settled on (a container resolves to a
     *  leaf), once known. Ahead of [PlayerUiState.item] on a screen bound to
     *  the service's item, which shows before the item has loaded. */
    private var preparedItemId: String? = null

    /** Live playback mode reported with progress beacons — feeds the
     *  analytics direct-vs-transcode split. Updated on source selection and
     *  on every (re)started transcode session, so mid-watch switches
     *  (audio-stream change, quality fallback) report what is actually
     *  happening. */
    private var activeDecision: String? = null
    private var transcodeToken: String? = null
    var hlsOffsetMs: Long = 0L
        private set

    /** Full CONTENT duration in ms, independent of the current session, or
     *  [ContentDuration.UNKNOWN] — which the progress reports send without a
     *  duration. One answer for the progress reports and the Up Next lead-in.
     *
     *  Progress beacons send a content-ABSOLUTE position (position + offset).
     *  Pairing that with anything shorter than the content makes the server
     *  see a ratio far past the real one, marking things watched (and
     *  scrobbling) early and clearing the resume point. This used to fall
     *  back to the player's duration whenever the ITEM carried none — which
     *  the API often omits although the file has one — and on an HLS session
     *  that is only what the server has produced so far: a movie resumed over
     *  HLS was marked watched about four minutes in. Now the player's only
     *  when [playerDurationTrusted], then the playing file's, then the item's
     *  listed runtime (see ContentDuration). */
    fun contentDurationMs(playerDurationMs: Long, playerDurationTrusted: Boolean): Long {
        val item = _state.value.item
        return ContentDuration.of(
            itemDurationMs = item?.duration_ms,
            fileDurationMs = item?.files?.firstOrNull()?.duration_ms,
            playerDurationMs = playerDurationMs,
            playerDurationTrusted = playerDurationTrusted,
        )
    }

    // Cache the inputs needed to re-issue a transcode session when
    // the user picks a different audio track. ExoPlayer's
    // setPreferredAudioLanguage works for direct play, but a
    // transcoded HLS stream only carries the one audio the server
    // picked at start time — switching languages means a fresh
    // session at the same byte position with a new audio_stream_index.
    private var lastTranscodeRequest: TranscodeRequest? = null

    private data class TranscodeRequest(
        val itemId: String,
        val fileId: String,
        val height: Int,
        val videoCopy: Boolean,
        val serverUrl: String,
    )

    /** [fromStart]: ignore the item's resume point — album / artist Play
     *  starts track 1 at 0:00 even if a partial play left one.
     *  [startAtMs]: start exactly there instead (an audiobook bookmark);
     *  only for a playable item, not a container Play resolves to a leaf. */
    fun prepare(itemId: String, fromStart: Boolean = false, startAtMs: Long? = null) {
        // A timer the previous screen handed over when it followed the
        // background queue here (see handOffSleepTimer).
        adoptSleepTimer(itemId)
        preparedItemId = null
        // An audio switch still starting for what played before brings a
        // session nobody will play: it stops it when it lands.
        audioSwitch = null
        if (backgroundItemId() == itemId) {
            bindToServiceItem(itemId, startAtMs)
            return
        }
        // This screen reports progress for what it plays (a service-bound one
        // leaves that to PlaybackService). Asked once per server — cached —
        // and never held up for: the first report is ten seconds off.
        viewModelScope.launch {
            progressWithoutDuration = orNull { serverCapabilities.progressWithoutDuration() } ?: false
        }
        val requestedId = itemId
        viewModelScope.launch {
            try {
                // Neither depends on which item plays, so both are in flight
                // while the item loads rather than after it. Neither can fail
                // the prepare: preferences are optional and the watch limit
                // fails open.
                val prefsCall = async { orNull { preferencesRepo.get() } }
                val limitCall = async { orNull { watchLimitRepo.get() } }
                // Container items (show / season / album / artist /
                // audiobook / podcast / anime) carry no files of their
                // own — only their children do. Hitting Play on a show
                // tile means "play the next episode" so we walk down to
                // a leaf before doing anything else.
                var resolvedId = itemId
                var item = try {
                    itemRepo.getItem(resolvedId)
                } catch (e: Exception) {
                    // Offline path: server unreachable, but the user
                    // may have downloaded this exact item. Look in the
                    // manifest for a completed download keyed to this
                    // item id and synthesise just enough ItemDetail /
                    // ItemFile to play it from disk. Bypasses the
                    // server entirely — same as the airplane-mode
                    // playback path on Spotify / Plex.
                    //
                    // ONLY when the server could not be reached. A server that
                    // ANSWERED with an auth/access refusal (a content-rating
                    // ceiling lowered after download, library access revoked,
                    // item gone) is an authoritative "no" — playing the local
                    // copy there would bypass the very gate that just refused.
                    // Such errors fall through to the catch below and surface
                    // the restricted / not-found message instead.
                    if (isServerRefusal(e)) throw e
                    val offline = playFromLocalIfDownloaded(resolvedId)
                    if (offline != null) return@launch
                    throw e
                }
                if (item.files.isEmpty() && isContainerType(item.type)) {
                    val leafId = resolveLeafToPlay(resolvedId)
                    if (leafId != null && leafId != resolvedId) {
                        resolvedId = leafId
                        item = itemRepo.getItem(resolvedId)
                    }
                }
                val file = item.files.firstOrNull()
                if (file == null) {
                    _state.value = PlayerUiState(loading = false, error = "No playable file")
                    return@launch
                }
                val itemId = resolvedId
                preparedItemId = itemId
                // Server-authoritative play decision (capability profiles). The
                // device's X-Client-Capabilities header (AuthInterceptor) tells the
                // server what it can decode; map the verdict to a PlaybackMode and
                // fall back to the local PlaybackHelper when the server is
                // unreachable. ExoPlayer range-requests, so no faststart refinement.
                // It and the markers need only the resolved item, so they load
                // side by side (and alongside the two calls above).
                val verdictCall = async { orNull { transcodeRepo.decide(itemId, file.id) } }
                val markersCall = async { orNull { itemRepo.getMarkers(itemId) }.orEmpty() }
                val serverUrl = serverPrefs.getServerUrl()?.trimEnd('/').orEmpty()
                val explicitStart = startAtMs?.takeIf { it >= 0 && resolvedId == requestedId }
                val startMs = explicitStart ?: if (fromStart) 0L else item.view_offset_ms

                // Offline-first: if the user has a completed download
                // for this file, play the local copy. Skips even the
                // transcode/remux negotiation — the on-disk file is
                // the original bytes the server has.
                downloads.store.load()
                val downloaded = downloads.store.get(file.id)
                val localFile = downloaded?.takeIf { it.status == "completed" }
                    ?.let { downloads.store.fileFor(it) }
                    ?.takeIf { it.exists() && it.length() > 0 }

                val verdict = verdictCall.await()
                val mode = run {
                    val resolved = when (verdict) {
                        "directPlay" -> PlaybackMode.DirectPlay
                        "directStream" -> PlaybackMode.Remux
                        "transcode" -> PlaybackMode.Transcode(
                            if ((file.resolution_h ?: 1080) >= 2160) 2160 else 1080
                        )
                        // "unsupported" (Dolby Vision) is handled below before use;
                        // null/unknown → local fallback (server unreachable).
                        else -> PlaybackHelper.decide(file)
                    }
                    android.util.Log.i(
                        "PlayerViewModel",
                        "playback decision: server=$verdict -> $resolved (${file.video_codec}/${file.audio_codec})",
                    )
                    resolved
                }

                // Parental watch-limit pre-flight — block a restricted user
                // before any stream/transcode starts. Runs for local downloads
                // too: reaching this line means getItem() just succeeded, so
                // the server IS reachable and skipping the check only let a
                // downloaded copy start past an exhausted limit. (The truly
                // offline path returned earlier via playFromLocalIfDownloaded.)
                // Fail-open if the check errors; the progress 403 still catches
                // a cap reached mid-session. Nothing has started streaming yet:
                // the decision call only asks.
                val wl = limitCall.await()
                if (wl != null && !wl.allowed) {
                    _state.value = PlayerUiState(loading = false, error = parentalBlockMessage(wl.reason))
                    return@launch
                }

                // Dolby Vision is not supported: the server returns the "unsupported"
                // verdict (DV can't be tonemapped correctly server-side — see
                // docs/dolby-vision.md). Show a clear message instead of a broken
                // transcode. A completed local download still plays (ExoPlayer decodes
                // DV on-device), so this only gates server streaming; the hdr_type
                // check covers a failed/absent decision call.
                if (localFile == null && (verdict == "unsupported" ||
                        file.hdr_type?.equals("dolby_vision", ignoreCase = true) == true)) {
                    _state.value = PlayerUiState(loading = false, error = "Dolby Vision is not supported")
                    return@launch
                }

                // The background service already has this item current — a
                // container (album, book) resolved to the track it is playing,
                // or its queue moved onto the item while this loaded. (Opened
                // on the item itself, prepare() binds without any of this —
                // see bindToServiceItem.) The screen only binds to the service,
                // which plays the file directly, so a server remux/transcode
                // started here would be an ffmpeg session nobody ever reads.
                val playingInService = backgroundItemId() == itemId

                val source = when {
                    localFile != null -> {
                        hlsOffsetMs = 0
                        lastTranscodeRequest = null
                        activeDecision = "directPlay"
                        PlaybackSource.DirectPlay("file://${localFile.absolutePath}", startMs)
                    }
                    mode is PlaybackMode.DirectPlay || playingInService -> {
                        hlsOffsetMs = 0
                        lastTranscodeRequest = null
                        activeDecision = "directPlay"
                        PlaybackSource.DirectPlay(
                            buildDirectPlayUrl(serverUrl, file.stream_url, file.stream_token),
                            startMs,
                        )
                    }
                    mode is PlaybackMode.Remux ->
                        startTranscode(itemId, 0, startMs, file.id, true, serverUrl)
                    mode is PlaybackMode.Transcode ->
                        startTranscode(itemId, mode.height, startMs, file.id, false, serverUrl)
                    else -> error("unreachable")
                }

                val markers = markersCall.await()
                val prefs = prefsCall.await()
                // Side-load urls for the subtitles: every text one on a remux /
                // transcode (that HLS has no text of its own), the attached
                // files on direct play. A service-bound screen loads none.
                val subtitleTracks = if (playingInService) {
                    SubtitleTracks.build("", file, assetToken = null, sideLoadEmbedded = false)
                } else {
                    SubtitleTracks.build(
                        serverUrl, file, subtitleAssetToken(file, source is PlaybackSource.Hls),
                        sideLoadEmbedded = source is PlaybackSource.Hls,
                    )
                }

                _state.value = PlayerUiState(
                    loading = false,
                    source = source,
                    item = item,
                    audioStreams = file.audio_streams,
                    // Started with no audio_stream_index: the server's default,
                    // the file's first audio stream (`-map 0:a:0`).
                    sessionAudioRow = if (source is PlaybackSource.Hls && file.audio_streams.isNotEmpty()) 0 else null,
                    subtitles = file.subtitle_streams,
                    subtitleTracks = subtitleTracks,
                    markers = markers,
                    preferredAudioLang = prefs?.preferred_audio_lang,
                    preferredSubtitleLang = prefs?.preferred_subtitle_lang,
                    forcedSubtitlesOnly = prefs?.forced_subtitles_only ?: false,
                    playingInService = playingInService,
                )

                loadItemExtras(item, itemId, streamed = localFile == null)
                subscribeRemoteProgress(itemId)
                subscribeAdminStops(itemId)
            } catch (e: Exception) {
                _state.value = PlayerUiState(loading = false, error = failureMessage(e))
            }
        }
    }

    /**
     * Reopening the full player over the item PlaybackService already has
     * current: the mini player, the notification, or a detail page's Play on
     * the track that is playing.
     *
     * This used to run the whole cold path first — the item, preferences, the
     * play decision, the watch limit and the markers, one call after another —
     * and only then build the MediaController, so the screen sat on a black
     * spinner for seconds and taps were lost. None of it is needed here. The
     * decision was thrown away for a service item anyway (it plays the file
     * directly); preferences and markers only feed a screen-owned video
     * player; and the service's heartbeat enforces a revoked library or an
     * admin stop (HeartbeatRefusal). So publish at once, showing the copy of
     * the item fetched last if there is one, and refresh the item behind it.
     *
     * The watch limit is asked behind the publish too, with the item. The
     * heartbeat only speaks once the audio plays: a paused book reopened past
     * an exhausted limit offered a Play that ran ten seconds before it was
     * cut off, where the cold path shows the block up front.
     *
     * The source is published ONCE and kept: rememberAudioController is keyed
     * on it, and a new instance would rebind the controller. The screen only
     * binds (see [PlayerUiState.playingInService]); the source's position is
     * where a bookmark jumps, and its url is the service's own queue
     * placeholder for the item, never a url built from a cached copy whose
     * stream token may have expired.
     */
    private fun bindToServiceItem(itemId: String, startAtMs: Long?) {
        preparedItemId = itemId
        hlsOffsetMs = 0
        lastTranscodeRequest = null
        activeDecision = "directPlay"
        val cached = itemRepo.cachedItem(itemId)
        val source = PlaybackSource.DirectPlay(
            MusicQueue.placeholderUri(itemId),
            startAtMs?.takeIf { it >= 0 } ?: 0L,
        )
        _state.value = PlayerUiState(
            loading = false,
            source = source,
            item = cached,
            audioStreams = cached?.files?.firstOrNull()?.audio_streams.orEmpty(),
            subtitles = cached?.files?.firstOrNull()?.subtitle_streams.orEmpty(),
            // No side-load urls: the service's player loads nothing but the file.
            subtitleTracks = SubtitleTracks.build("", cached?.files?.firstOrNull(), assetToken = null, sideLoadEmbedded = false),
            playingInService = true,
        )
        if (cached != null) loadItemExtras(cached, itemId, streamed = true)
        subscribeRemoteProgress(itemId)
        subscribeAdminStops(itemId)
        viewModelScope.launch {
            // Fails open, as on the cold path.
            val limitCall = async { orNull { watchLimitRepo.get() } }
            val fresh = try {
                itemRepo.getItem(itemId)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // The server refusing the item is its answer, shown as on the
                // cold path (the service's heartbeat stops the audio). Anything
                // else — offline, a 5xx — leaves the screen on what it has.
                if (isServerRefusal(e) && _state.value.source === source) {
                    _state.update { it.copy(error = failureMessage(e)) }
                }
                return@launch
            }
            // Another prepare() took over while this loaded.
            if (_state.value.source !== source) return@launch
            val wl = limitCall.await()
            if (_state.value.source !== source) return@launch
            if (wl != null && !wl.allowed) {
                _state.update { it.copy(item = fresh, error = parentalBlockMessage(wl.reason)) }
                return@launch
            }
            val file = fresh.files.firstOrNull()
            _state.update {
                it.copy(
                    item = fresh,
                    audioStreams = file?.audio_streams.orEmpty(),
                    subtitles = file?.subtitle_streams.orEmpty(),
                    subtitleTracks = SubtitleTracks.build("", file, assetToken = null, sideLoadEmbedded = false),
                )
            }
            if (cached == null) loadItemExtras(fresh, itemId, streamed = true)
        }
    }

    /** The best-effort loads that follow once [item] is showing. [streamed]:
     *  not a local download (trickplay and lyrics need the server). */
    private fun loadItemExtras(item: ItemDetail, itemId: String, streamed: Boolean) {
        loadArtwork(item)

        // Audiobooks: the book's listening speed (and whether the
        // server takes bookmarks). An end-of-chapter timer set before
        // the item loaded, or handed over from the previous screen, can
        // watch the chapter marks now that they're here.
        loadListeningSpeed(item)
        if (_sleepTimer.value?.mode == SleepTimer.EndOfChapter) startChapterWatch()

        // Trickplay cues — best-effort. If the server hasn't
        // generated thumbnails yet (status != "done"), the
        // status fetch returns a `not_started` sentinel and
        // we skip the VTT fetch entirely. Local-file playback
        // (offline) also skips since trickplay endpoints need
        // a live server.
        if (streamed) {
            loadTrickplayCues(itemId)
        }

        // Lyrics — only meaningful for tracks. Server 404s
        // for non-tracks anyway; the repo maps that to null.
        // Best-effort, no error surface — overlay just doesn't
        // appear when there are none.
        if (item.type == "track" && streamed) {
            loadLyrics(itemId)
        }

        // Episode + track auto-advance: same parent + index
        // relationship on both sides. The screen branches on
        // item.type to decide whether to surface an overlay
        // (episodes) or chain silently (music tracks).
        val parentId = item.parent_id
        val index = item.index
        val type = item.type
        if (parentId != null && index != null && (type == "episode" || type == "track")) {
            viewModelScope.launch { loadNextSibling(item.id, parentId, index, type) }
        }
    }

    /** What the screen shows for a prepare that failed with [e]. Never null:
     *  a null error left the screen on its spinner for good. */
    private fun failureMessage(e: Exception): String =
        if (e is HttpException && e.code() == 403) {
            // 403 covers three gates: the content-rating ceiling, the
            // parental watch limit, and an admin stop (a restart inside
            // the stop window — transcode start answers PLAYBACK_STOPPED).
            // Parse the code so each shows right.
            val err = e.apiError()
            when (err?.code) {
                "PARENTAL_LIMIT" -> parentalBlockMessage(err.message)
                PlaybackStop.ERROR_CODE -> PlaybackStop.textFromServer(err.message)
                else -> "content_restricted"
            }
        } else {
            // Any other refusal says why in its error body — a transcode start
            // on a file the server can't read answers 422 "This file appears
            // to be corrupt …". Retrofit's own message is the bare "HTTP 422",
            // which is all this screen used to show.
            (e as? HttpException)?.apiError()?.message?.takeIf { it.isNotBlank() }
                ?: e.message?.takeIf { it.isNotBlank() }
                ?: START_FAILED_MESSAGE
        }

    /** Offline fallback: walk the download manifest for a completed
     *  entry whose item_id matches [itemId], and if one exists, build
     *  a minimal PlayerUiState pointing at the on-disk file. Returns
     *  the synthesised state on success (already published to
     *  `_state`), or null when no usable local copy is present.
     *
     *  The synthesised ItemFile carries no audio/subtitle stream
     *  metadata since the manifest doesn't store it — pickers just
     *  hide the buttons, which matches Plex's offline-mode UI. */
    private suspend fun playFromLocalIfDownloaded(itemId: String): PlayerUiState? {
        downloads.store.load()
        val entry = downloads.store.state.value.entries.firstOrNull {
            it.item_id == itemId && it.status == "completed"
        } ?: return null
        val localFile = downloads.store.fileFor(entry).takeIf { it.exists() && it.length() > 0 }
            ?: return null
        // Diagnostic: log the file's first 32 bytes + length so we
        // can confirm the worker wrote real media (MKV starts with
        // 1A 45 DF A3; MP4 has "ftyp" at offset 4) vs an HTML body
        // or a truncated stream. Visible in logcat under
        // PlayerViewModel.
        try {
            // readNBytes(int) is API 33+; do it the portable way for
            // minSdk 24. read(byteArray) returns -1 at EOF (file
            // shorter than 32 bytes) so we trim before formatting.
            val buf = ByteArray(32)
            val n = localFile.inputStream().use { stream ->
                var read = 0
                while (read < buf.size) {
                    val r = stream.read(buf, read, buf.size - read)
                    if (r <= 0) break
                    read += r
                }
                read
            }
            val hex = buf.take(n).joinToString(" ") { "%02X".format(it) }
            android.util.Log.i(
                "PlayerViewModel",
                "offline play: ${localFile.absolutePath} size=${localFile.length()} head=$hex",
            )
        } catch (_: Exception) {
        }
        hlsOffsetMs = 0
        lastTranscodeRequest = null
        val syntheticFile = tv.onscreen.mobile.data.model.ItemFile(
            id = entry.file_id,
            stream_url = "",
            container = entry.container,
        )
        val syntheticItem = tv.onscreen.mobile.data.model.ItemDetail(
            id = entry.item_id,
            library_id = "",
            title = entry.item_title,
            type = entry.item_type,
            poster_path = entry.poster_path,
            files = listOf(syntheticFile),
        )
        val state = PlayerUiState(
            loading = false,
            source = PlaybackSource.DirectPlay("file://${localFile.absolutePath}", 0L),
            item = syntheticItem,
        )
        _state.value = state
        // Offline, so the lookup fails — but a speed picked earlier in this
        // session is still known locally.
        loadListeningSpeed(syntheticItem)
        return state
    }

    // ── Audiobooks: listening speed + bookmarks ───────────────────────

    /** Look up the speed of the book [item] belongs to. Nothing for
     *  anything that isn't an audiobook — it plays at 1×. */
    private fun loadListeningSpeed(item: ItemDetail) {
        val bookId = AudiobookSpeed.bookIdOf(item.type, item.id, item.parent_id) ?: return
        viewModelScope.launch {
            val speed = audiobooks.listeningSpeed(item.id, bookId)
            if (_state.value.item?.id != item.id) return@launch
            _state.value = _state.value.copy(
                listeningRate = speed.rate ?: AudiobookSpeed.NORMAL,
                bookmarksSupported = speed.serverSupport,
            )
        }
    }

    /** The listener picked a speed. The screen has already applied it to
     *  the player; this records it and saves it for the book (fire-and-
     *  forget — a failed save still leaves it in effect on this device). */
    fun setListeningRate(rate: Float) {
        val item = _state.value.item ?: return
        val bookId = AudiobookSpeed.bookIdOf(item.type, item.id, item.parent_id) ?: return
        val clamped = AudiobookSpeed.clamp(rate)
        _state.value = _state.value.copy(listeningRate = clamped)
        audiobooks.saveRate(item.id, bookId, clamped)
    }

    private val _bookmarkNotices = Channel<BookmarkNotice>(Channel.BUFFERED)

    /** One notice per "Add bookmark", for a toast. */
    val bookmarkNotices: Flow<BookmarkNotice> = _bookmarkNotices.receiveAsFlow()

    /** Bookmark [positionMs] (content time) in the item being played — the
     *  book for a single-file book, the chapter for a multi-file one. */
    fun addBookmark(positionMs: Long, note: String) {
        val item = _state.value.item ?: return
        if (!AudiobookSpeed.hasSpeed(item.type)) return
        viewModelScope.launch {
            val notice = try {
                BookmarkNotice.Added(audiobooks.addBookmark(item.id, positionMs, note).position_ms)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                when ((e as? HttpException)?.code()) {
                    409 -> BookmarkNotice.LimitReached
                    404 -> {
                        _state.value = _state.value.copy(bookmarksSupported = false)
                        BookmarkNotice.Unsupported
                    }
                    else -> BookmarkNotice.Failed
                }
            }
            _bookmarkNotices.trySend(notice)
        }
    }

    private fun isContainerType(type: String?): Boolean = type in setOf(
        "show", "season", "anime", "album", "artist", "audiobook", "podcast",
    )

    /** Walk a container down to the leaf the user expects when they
     *  hit Play. Mirrors the Plex / Jellyfin "next-up" rule:
     *   1. Resume an in-progress leaf if one exists.
     *   2. Otherwise play the leaf immediately after the last fully-
     *      watched one.
     *   3. Otherwise the first unwatched leaf.
     *   4. Otherwise (whole container watched) the very first leaf —
     *      replay from the start. */
    private suspend fun resolveLeafToPlay(containerId: String): String? {
        val children = try {
            itemRepo.getChildren(containerId)
        } catch (_: Exception) {
            return null
        }
        if (children.isEmpty()) return null
        val sorted = children.sortedWith(ChildItem.PLAY_ORDER)

        // show / anime / artist nest one level deeper. Flatten by
        // pulling each container child's own children in order so the
        // pickNextUp pass sees a single ordered episode / track list.
        val leaves = if (sorted.any { isContainerType(it.type) }) {
            val flat = mutableListOf<tv.onscreen.mobile.data.model.ChildItem>()
            for (c in sorted) {
                if (isContainerType(c.type)) {
                    val grandKids = try {
                        itemRepo.getChildren(c.id)
                    } catch (_: Exception) {
                        emptyList()
                    }
                    flat.addAll(grandKids.sortedWith(ChildItem.PLAY_ORDER))
                } else {
                    flat.add(c)
                }
            }
            flat
        } else {
            sorted
        }
        if (leaves.isEmpty()) return null

        leaves.firstOrNull { it.view_offset_ms > 0 && !it.watched }?.let { return it.id }
        val lastWatchedIdx = leaves.indexOfLast { it.watched }
        if (lastWatchedIdx >= 0 && lastWatchedIdx + 1 < leaves.size) {
            return leaves[lastWatchedIdx + 1].id
        }
        leaves.firstOrNull { !it.watched }?.let { return it.id }
        return leaves.first().id
    }

    private suspend fun loadNextSibling(itemId: String, parentId: String, currentIndex: Int, type: String) {
        try {
            val children = itemRepo.getChildren(parentId)
            val next = NextSiblingResolver.nextInContainer(children, itemId, type, currentIndex)
            _state.value = _state.value.copy(nextSibling = next)
        } catch (_: Exception) {
            // Best-effort.
        }
    }

    private suspend fun startTranscode(
        itemId: String,
        height: Int,
        posMs: Long,
        fileId: String,
        videoCopy: Boolean,
        serverUrl: String,
    ): PlaybackSource {
        stopActiveTranscode()
        val req = TranscodeRequest(itemId, fileId, height, videoCopy, serverUrl)
        return adoptSession(requestSession(req, posMs, audioStreamIndex = null), req, posMs)
    }

    /** Ask the server for a session of [req] opening at [posMs], with audio
     *  row [audioStreamIndex] (null: the server's default, the first). The
     *  screen plays none of it until [adoptSession]. */
    private suspend fun requestSession(req: TranscodeRequest, posMs: Long, audioStreamIndex: Int?): TranscodeSession =
        transcodeRepo.start(
            itemId = req.itemId,
            height = req.height,
            positionMs = posMs,
            fileId = req.fileId,
            videoCopy = req.videoCopy,
            audioStreamIndex = audioStreamIndex,
            supportsHevc = PlaybackHelper.supportsHevc(),
        )

    /** Make [session] the one this screen plays, reports and retires, and
     *  return its source. */
    private fun adoptSession(session: TranscodeSession, req: TranscodeRequest, posMs: Long): PlaybackSource {
        transcodeSessionId = session.session_id
        transcodeToken = session.token
        // Where the session REALLY opens, not where it was asked to: a remux
        // copies the video, so it can only start on a keyframe — up to several
        // seconds earlier on a sparse-GOP rip — and the server reports that
        // point as start_offset_sec. Everything content-timed on this screen
        // is position + hlsOffsetMs: the progress reports, skip markers,
        // chapters, and now the side-loaded subtitles, whose cues would show
        // seconds early against the picture with the requested offset. A real
        // 0 is kept: resuming a few seconds in, before the first keyframe
        // after 0:00, opens the session at the very start, and an ABR ladder's
        // stream always covers the whole file from 0:00 — the player seeks to
        // the request in either. Only an older server, which sends no field
        // at all, falls back to the request.
        hlsOffsetMs = session.start_offset_sec?.takeIf { it >= 0.0 }
            ?.let { (it * 1000.0).toLong() }
            ?: posMs
        lastTranscodeRequest = req
        // videoCopy = remux session (original video bits, container rewrap) —
        // the server calls that verdict directStream.
        activeDecision = if (req.videoCopy) "directStream" else "transcode"

        // The player starts at posMs within it (see PlaybackSource.Hls.startMs),
        // on an audio-track switch's new session as on the first.
        return PlaybackSource.Hls("${req.serverUrl}${session.playlist_url}", hlsOffsetMs, posMs)
    }

    /** The source an audio-track switch replaced, until its player's
     *  progress reporter has let go of it. */
    private var swappedOutSource: PlaybackSource? = null

    /**
     * Whether [source] was replaced by an audio-track switch rather than
     * ended — the screen's progress reporter asks as that source's player goes
     * away, and then sends nothing: playback carries on in the new session.
     *
     * Not 'stopped': the server drops every stream session of the item on a
     * 'stopped' report — the one just started for the new track included — so
     * the new player would run dry once it had played what it had buffered
     * (it is also a scrobble 'stop'). Not 'paused' either: that fires the
     * pause webhooks, and Trakt would get a pause and a fresh start for one
     * uninterrupted watch. The new player's first beat moves the position on
     * within ten seconds. Answers once.
     */
    fun swappedOut(source: PlaybackSource): Boolean {
        if (swappedOutSource !== source) return false
        swappedOutSource = null
        return true
    }

    /** Re-issue the active transcode session with the audio of picker row
     *  [audioRow] — its position in [PlayerUiState.audioStreams], which is
     *  the server's audio-relative audio_stream_index, never
     *  AudioStream.index (see AudioSelection) — preserving the current
     *  position. Direct-play swaps tracks via the player's track selector
     *  and never comes through here. */
    fun switchAudioStream(audioRow: Int, currentPositionMs: Long) {
        val req = lastTranscodeRequest ?: return
        val ui = _state.value
        // Not a row: it names no audio stream of this file, and a session
        // asked for one ends with no playlist at all — a playback error a
        // few seconds later, the old session already retired.
        if (audioRow !in ui.audioStreams.indices) {
            android.util.Log.w(
                "PlayerViewModel",
                "ignoring audio switch: row $audioRow of ${ui.audioStreams.size} audio stream(s)",
            )
            return
        }
        // Already the row playing, or the one on its way: a new session would
        // only rebuffer. The session's row changes only once the replacement
        // has started — seconds on a cold ffmpeg start — and checked against
        // that alone, a pick back to the playing row meanwhile was dropped as
        // "already playing" (the switch then landed on the row just left), and
        // picking the pending row again started a second switch alongside it.
        //
        // A pick back to the session's row does start a session: the server
        // keeps one per viewer and item, so the start in flight has already
        // retired the live one, whose player only has its buffer left.
        if (audioRow == ui.targetAudioRow) return
        // The newest pick wins: a switch still starting is superseded.
        audioSwitch = null
        _state.update { it.copy(pendingAudioRow = audioRow) }
        val posMs = currentPositionMs + hlsOffsetMs
        // Lazy, so [audioSwitch] names it before it runs (see runAudioSwitch).
        val switch = viewModelScope.launch(start = CoroutineStart.LAZY) {
            runAudioSwitch(coroutineContext.job, req, audioRow, posMs)
        }
        audioSwitch = switch
        switch.start()
    }

    /** The audio switch still starting its session — the latest pick's — or
     *  null. One superseded by a later pick (or by a new prepare(), or an
     *  admin stop) is no longer it, and plays nothing. */
    private var audioSwitch: Job? = null

    /**
     * Start [audioRow]'s session at [posMs] and, if [self] is still the
     * current switch when it is in hand, play it.
     *
     * The live session is held open across the request and retired only once
     * the replacement is in hand, so a failed switch leaves the old track
     * playing and marked. A superseded switch is let run, not cancelled:
     * cancelling the request can lose a session the server has already
     * started, where letting it land hands over its id — and its own stop
     * retires it unplayed. Two quick picks used to leak the first one's
     * session, and the later pick to land played, whichever it was.
     */
    private suspend fun runAudioSwitch(self: Job, req: TranscodeRequest, audioRow: Int, posMs: Long) {
        val session = try {
            requestSession(req, posMs, audioStreamIndex = audioRow)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // An admin stop refuses a replacement session for the stop
            // window; that's the end of playback, not a failed switch — for
            // whichever pick it answers.
            val stopped = (e as? HttpException)?.takeIf { it.code() == 403 }?.apiError()
                ?.takeIf { it.code == PlaybackStop.ERROR_CODE }
            if (stopped != null) {
                stopForAdmin(PlaybackStop.textFromServer(stopped.message))
                return
            }
            android.util.Log.w("PlayerViewModel", "audio stream switch failed", e)
            // A superseded switch's failure leaves the later pick's row alone.
            if (audioSwitch === self) {
                audioSwitch = null
                _state.update { it.copy(pendingAudioRow = null) }
            }
            return
        }
        if (audioSwitch !== self) {
            transcodeRepo.stopDetached(session.session_id, session.token)
            return
        }
        audioSwitch = null
        val prevSession = transcodeSessionId
        val prevToken = transcodeToken
        val source = adoptSession(session, req, posMs)
        // Publishing the new source ends the old player; its terminal
        // report must not go out (see [swappedOut]).
        swappedOutSource = _state.value.source
        _state.update { it.copy(source = source, sessionAudioRow = audioRow, pendingAudioRow = null) }
        if (prevSession != null && prevToken != null) {
            transcodeRepo.stopDetached(prevSession, prevToken)
        }
    }

    /** True when [e] is the server definitively refusing the item (it
     *  answered, with 401/403/404/410) rather than a transport failure.
     *  Offline fallback must never trigger on these. 5xx stays eligible:
     *  a broken-but-reachable server is closer to "offline" than to "no". */
    private fun isServerRefusal(e: Exception): Boolean =
        e is HttpException && e.code() in setOf(401, 403, 404, 410)

    /** Map a server PARENTAL_LIMIT reason to a friendly sentence. The screen
     *  renders [PlayerUiState.error] verbatim, so this returns display text. */
    private fun parentalBlockMessage(reason: String?): String = when (reason) {
        "outside_allowed_hours" ->
            "Playback is outside the allowed hours for this account. Try again during the permitted times."
        "daily_limit_reached" ->
            "You’ve reached today’s watch-time limit for this account. Check back tomorrow."
        else -> "Playback is blocked by a parental watch limit on this account."
    }

    /** Fire-and-forget progress publish. Best-effort: server
     *  unreachability shouldn't crash playback, and the next tick
     *  will pick up where this one left off. Runs on viewModelScope —
     *  fine for the periodic 'playing' heartbeat, which should stop
     *  when playback does. Terminal 'stopped' must use
     *  [reportProgressFinal] instead (see its note).
     *
     *  [durationMs] [ContentDuration.UNKNOWN]: on a server that says it keeps
     *  the duration it knows when a report leaves it out
     *  ([progressWithoutDuration]), the beat still goes, without one. Skipping
     *  it meant a parental watch limit never counted the time (the server
     *  accrues it per 'playing' beat), the beat's own admin-stop and access
     *  refusals never came, and Now Playing showed a frozen position. Any
     *  other server stores each report's duration as sent — the missing one
     *  over the known one, so the item read "unwatched" and left Continue
     *  Watching — and there the beat is skipped, as it always was. */
    fun reportProgress(itemId: String, positionMs: Long, durationMs: Long, state: String) {
        if (durationMs <= 0 && !progressWithoutDuration) return
        localProgressMs = positionMs
        LocalProgressTracker.record(itemId, positionMs)
        val duration = durationMs.takeIf { it > 0 }
        viewModelScope.launch {
            try {
                itemRepo.updateProgress(itemId, positionMs, duration, state, activeDecision)
            } catch (e: Exception) {
                // A 'playing' heartbeat rejected with a parental watch-limit
                // 403 means the cap was reached (or the allowed-hours window
                // closed) mid-session. Surface the block — setting error tears
                // down the player host and shows the message. Other failures
                // stay best-effort.
                //
                // ANY 403 on a 'playing' heartbeat stops playback, not just
                // PARENTAL_LIMIT: a content-rating / library-access 403 means
                // the server no longer lets this user watch the item, and
                // ignoring it let an offline-fallback copy (or a stream on an
                // already-issued token) keep playing while online. The
                // decision is shared with PlaybackService's background
                // heartbeat — see HeartbeatRefusal.
                when (val refusal = HeartbeatRefusal.of(state, e)) {
                    null -> Unit
                    is HeartbeatRefusal.WatchLimit ->
                        _state.value = _state.value.copy(error = parentalBlockMessage(refusal.reason))
                    is HeartbeatRefusal.PlaybackStopped ->
                        stopForAdmin(refusal.message)
                    HeartbeatRefusal.ContentRevoked ->
                        _state.value = _state.value.copy(error = "content_restricted")
                }
            }
        }
    }

    /** Terminal 'stopped' publish, routed through the repository's
     *  app-lifetime scope rather than viewModelScope. The player VM is
     *  cleared the moment an auto-advancing track pops the screen off
     *  the back stack, so a viewModelScope.launch here would be
     *  cancelled before the PUT lands — dropping the 'stop' watch-event
     *  the server scrobbles on, so a completed music track would never
     *  reach ListenBrainz.
     *
     *  An unknown [durationMs] goes out as none where the heartbeat's would
     *  (and is skipped where it would be): the report still records where
     *  playback stopped, ends the stream in Now Playing and drops its server
     *  session. With no duration anywhere the server has no ratio to call the
     *  item watched by, so it isn't. */
    fun reportProgressFinal(itemId: String, positionMs: Long, durationMs: Long) {
        if (durationMs <= 0 && !progressWithoutDuration) return
        itemRepo.reportProgressDetached(
            itemId, positionMs, durationMs.takeIf { it > 0 }, "stopped", activeDecision,
        )
    }

    /** Cleared by the screen after it consumes the seek signal so the
     *  same emission doesn't seek a second time on recomposition. */
    fun clearRemoteResume() {
        _remoteResumeMs.value = null
    }

    // ── Online subtitles (OpenSubtitles search) ──────────────────────────

    private val _onlineSubtitleSearch = MutableStateFlow(OnlineSubtitleSearchUi())
    val onlineSubtitleSearch: StateFlow<OnlineSubtitleSearchUi> = _onlineSubtitleSearch.asStateFlow()

    /** Run an OpenSubtitles search through the server-side proxy. The
     *  server keeps the API key + rate-limit budget; the client just
     *  passes through the user's filters. */
    fun searchOnlineSubtitles(itemId: String, lang: String?, query: String?) {
        _onlineSubtitleSearch.value = OnlineSubtitleSearchUi(loading = true)
        viewModelScope.launch {
            try {
                val results = onlineSubtitles.search(itemId, lang = lang, query = query)
                _onlineSubtitleSearch.value = OnlineSubtitleSearchUi(loading = false, results = results)
            } catch (e: Exception) {
                _onlineSubtitleSearch.value = OnlineSubtitleSearchUi(loading = false, error = e.message)
            }
        }
    }

    /** Download the chosen subtitle and attach it to the active file, then
     *  re-read the file's subtitles so it shows up in the picker right away:
     *  the server lists it under the file's external_subtitles, and the
     *  screen side-loads it into the running player (see PlayerHost). Not
     *  auto-selected — the user picks it from the subtitle sheet. */
    fun downloadOnlineSubtitle(itemId: String, candidate: OnlineSubtitle, onDone: () -> Unit) {
        val fileId = _state.value.item?.files?.firstOrNull()?.id ?: return
        viewModelScope.launch {
            try {
                onlineSubtitles.download(itemId, fileId, candidate)
                _onlineSubtitleSearch.value = OnlineSubtitleSearchUi()
                refreshSubtitleTracks(itemId, fileId)
                onDone()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                _onlineSubtitleSearch.value = _onlineSubtitleSearch.value.copy(error = e.message)
            }
        }
    }

    /** Re-read [fileId]'s subtitles from the server. Best-effort: on failure
     *  the list stays as it was (the download is still there next time). */
    private suspend fun refreshSubtitleTracks(itemId: String, fileId: String) {
        val fresh = orNull { itemRepo.getItem(itemId) } ?: return
        val file = fresh.files.firstOrNull { it.id == fileId } ?: return
        // Another prepare() took over meanwhile.
        if (_state.value.item?.files?.firstOrNull()?.id != fileId) return
        val serverUrl = serverPrefs.getServerUrl()?.trimEnd('/').orEmpty()
        val hls = _state.value.source is PlaybackSource.Hls
        val tracks = SubtitleTracks.build(
            serverUrl, file, subtitleAssetToken(file, hls), sideLoadEmbedded = hls,
        )
        _state.update { it.copy(subtitles = file.subtitle_streams, subtitleTracks = tracks) }
    }

    /** The asset token, when [file]'s subtitles need it: attached files take
     *  nothing else, and embedded streams (side-loaded only on an HLS
     *  session, [hls]) only lack a better one when the file has no stream
     *  token of its own. */
    private suspend fun subtitleAssetToken(
        file: tv.onscreen.mobile.data.model.ItemFile,
        hls: Boolean,
    ): String? =
        if (file.external_subtitles.isNotEmpty() ||
            (hls && file.stream_token.isNullOrEmpty() && file.subtitle_streams.isNotEmpty())
        ) {
            orNull { serverPrefs.getAssetToken() }
        } else {
            null
        }

    fun clearOnlineSubtitleSearch() {
        _onlineSubtitleSearch.value = OnlineSubtitleSearchUi()
    }

    /** Subscribe to `progress.updated` events for the currently-
     *  loaded item. The server broadcasts every progress write to all
     *  of a user's connected devices, so we have to filter:
     *   1. Wrong item — ignore.
     *   2. Position within ~3s of our last local report — same-device
     *      echo, ignore (otherwise the player fights its own writes).
     *   3. Otherwise — emit a seek-to so the screen can match the
     *      other device's position. */
    private fun subscribeRemoteProgress(itemId: String) {
        sseJob?.cancel()
        sseJob = viewModelScope.launch {
            try {
                notifications.subscribeProgressUpdates().collect { ev ->
                    if (ev.item_id != itemId) return@collect
                    // Compare against the last position ANY component of this
                    // app published for the item, not just this VM's own
                    // writes. Background audio is reported by PlaybackService,
                    // which this VM never sees — so its 10 s heartbeats echoed
                    // back looking like a remote device and seeked the player
                    // to where it already was, every tick, undoing any scrub
                    // the user made in between.
                    val localMs = LocalProgressTracker.lastFor(itemId) ?: localProgressMs
                    val delta = kotlin.math.abs(ev.position_ms - localMs)
                    if (delta < 3_000L) return@collect
                    _remoteResumeMs.value = ev.position_ms
                }
            } catch (_: Exception) {
                // NotificationsRepository re-dials SSE drops itself (5 s
                // backoff); anything reaching here leaves the player
                // running on its own state.
            }
        }
    }

    // ── Admin stop (Now Playing → Stop) ───────────────────────────────

    private var stopEventsJob: kotlinx.coroutines.Job? = null

    /** Obey the admin "stop this stream" SSE event for [itemId]. The channel
     *  is per-user, so every stop for any of the user's players arrives here;
     *  [tv.onscreen.mobile.data.model.PlaybackStopEvent.targets] picks ours —
     *  same item, and our transcode/remux session when the event names one.
     *  This client reports no client name in its heartbeats, so a stop aimed
     *  at another device by name never matches. The server also refuses the
     *  stopped stream for ~2 minutes (403 PLAYBACK_STOPPED on the 'playing'
     *  heartbeat — see [reportProgress]), so a missed event still stops us. */
    private fun subscribeAdminStops(itemId: String) {
        stopEventsJob?.cancel()
        stopEventsJob = viewModelScope.launch {
            try {
                notifications.subscribePlaybackStops().collect { ev ->
                    if (ev.targets(itemId, transcodeSessionId, clientName = null)) {
                        stopForAdmin(ev.displayText)
                    }
                }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (_: Exception) {
                // Stream failure — the heartbeat refusal is the backstop.
            }
        }
    }

    /** The screen-owned player hit a media request the server refused with
     *  403 PLAYBACK_STOPPED (see [playbackStoppedMessage]) — stop the same way
     *  the event and the heartbeat do. */
    fun onStreamRefusedByAdminStop(message: String) {
        stopForAdmin(message)
    }

    /** End playback because an admin stopped it, showing [text]. Setting
     *  [PlayerUiState.error] tears the player host down (which also sends the
     *  terminal 'stopped' report, like the web player saves progress before it
     *  stops); any server session is dropped too. First stop wins — the event
     *  and the heartbeat 403 usually both arrive. */
    private fun stopForAdmin(text: String) {
        if (_state.value.error != null) return
        stopEventsJob?.cancel()
        // An audio switch still starting brings a session nobody will play.
        audioSwitch = null
        stopActiveTranscode()
        _state.value = _state.value.copy(error = text, pendingAudioRow = null)
    }

    fun stopActiveTranscode() {
        val sid = transcodeSessionId ?: return
        val tok = transcodeToken ?: return
        transcodeSessionId = null
        transcodeToken = null
        // Detached: the common caller is onCleared(), where viewModelScope is
        // already closed — see TranscodeRepository.stopDetached.
        transcodeRepo.stopDetached(sid, tok)
    }

    /** Direct-play URL with a `?token=` carrier. ExoPlayer's
     *  DefaultHttpDataSource bypasses our OkHttp interceptor chain so
     *  a Bearer header isn't an option; the asset-route middleware
     *  (RequiredAllowQueryToken) accepts purpose-scoped tokens via the
     *  query string but rejects the general access token there.
     *
     *  Prefer the per-file 24h stream token; fall back to the 24h
     *  purpose=asset token (NOT the access token, which the server
     *  rejects in a URL). ExoPlayer can't refresh on a 401 mid-stream,
     *  so either long-lived token keeps a 90-min movie from dying with
     *  ERROR_CODE_IO_BAD_HTTP_STATUS.
     *
     *  The returned url carries NO credential — the token goes into
     *  [StreamTokenVault] and the player's resolving data source re-attaches
     *  it as the request leaves. For the audio branch this url becomes a
     *  MediaSession MediaItem, and media3 before 1.8 copied that uri into the
     *  platform session's METADATA_KEY_MEDIA_URI, readable by any
     *  notification-listener app; keeping the token out of the uri is what
     *  closes that (see StreamTokenVault). The video branch shares the
     *  builder and gets the same treatment via the resolver wrapper in
     *  PlayerScreen. */
    private suspend fun buildDirectPlayUrl(
        serverUrl: String,
        streamPath: String,
        streamToken: String?,
    ): String {
        val token = if (!streamToken.isNullOrEmpty()) streamToken else serverPrefs.getAssetToken()
        return StreamTokenVault.register("$serverUrl$streamPath", token)
    }

    override fun onCleared() {
        super.onCleared()
        // The timer dies with this screen; so does its "stop after this
        // item" request (only if still ours — a screen that followed the
        // queue may have armed its own).
        disarmStopAfterItem()
        sseJob?.cancel()
        stopEventsJob?.cancel()
        recycleSpriteCache()
        stopActiveTranscode()
    }

    /** Releases the native pixel memory backing each cached sprite
     *  sheet and empties the map. Called when the active item changes
     *  (loadTrickplayCues) and when the VM is destroyed (onCleared). */
    private fun recycleSpriteCache() {
        spriteCache.values.forEach { bm ->
            if (!bm.isRecycled) bm.recycle()
        }
        spriteCache.clear()
    }
}

/** Item types the player shows as audio, with a cover. A podcast is the
 *  show; the scanner files its playable episodes as podcast_episode. */
private val AUDIO_TYPES = setOf("track", "audiobook", "audiobook_chapter", "podcast", "podcast_episode")

/** Audio whose cover is its parent's: a track's album, a chapter's book, an
 *  episode's podcast. */
private val PARENT_ART_TYPES = setOf("track", "audiobook_chapter", "podcast_episode")

/** Cover width asked of the server: the player shows it up to the screen's
 *  width. The album and book pages ask the same, so the player finds the
 *  cover they showed in the image cache. */
private const val ARTWORK_WIDTH = 1080

/** A failed start that carries no message of its own. */
internal const val START_FAILED_MESSAGE = "Couldn’t start playback."

/** [block]'s result, or null if it fails — for the calls prepare() can do
 *  without. Inline, so [block] may suspend; a cancellation still propagates
 *  (swallowing it would keep a cleared screen's prepare running). */
private inline fun <T> orNull(block: () -> T): T? =
    try {
        block()
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        null
    }
