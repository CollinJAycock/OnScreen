package tv.onscreen.mobile.ui.player

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import retrofit2.HttpException
import tv.onscreen.mobile.data.model.AudioStream
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.Marker
import tv.onscreen.mobile.data.model.SubtitleStream
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.prefs.SubtitlePrefs
import tv.onscreen.mobile.data.prefs.SubtitleStyle
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.model.OnlineSubtitle
import tv.onscreen.mobile.data.repository.NotificationsRepository
import tv.onscreen.mobile.data.repository.OnlineSubtitleRepository
import tv.onscreen.mobile.data.repository.PreferencesRepository
import tv.onscreen.mobile.data.repository.TranscodeRepository
import tv.onscreen.mobile.data.repository.TrickplayRepository
import tv.onscreen.mobile.trickplay.TrickplayVtt
import javax.inject.Inject

sealed class PlaybackSource {
    data class DirectPlay(val url: String, val startMs: Long) : PlaybackSource()
    data class Hls(val playlistUrl: String, val offsetMs: Long) : PlaybackSource()
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
    val subtitles: List<SubtitleStream> = emptyList(),
    val markers: List<Marker> = emptyList(),
    val nextSibling: ChildItem? = null,
    val preferredAudioLang: String? = null,
    val preferredSubtitleLang: String? = null,
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
    val error: String? = null,
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
    private val downloads: tv.onscreen.mobile.data.downloads.OnScreenDownloadManager,
    private val notifications: NotificationsRepository,
    private val onlineSubtitles: OnlineSubtitleRepository,
    private val trickplayRepo: TrickplayRepository,
) : ViewModel() {

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
            spriteCache.clear()
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

    /** Start a wall-clock countdown sleep timer. Replaces any running
     *  timer. Off cancels. EndOfTrack switches to the content-aware
     *  mode (no countdown — UI shows "End of track" label and we
     *  arm a one-shot when the player emits Player.STATE_ENDED). */
    fun setSleepTimer(mode: SleepTimer) {
        sleepTimerJob?.cancel()
        sleepTimerJob = null
        _sleepTimerFired.value = false
        when (mode) {
            SleepTimer.Off -> {
                _sleepTimer.value = null
            }
            SleepTimer.EndOfTrack -> {
                // No countdown; just stash the mode so the player's
                // onPlaybackStateChanged listener can fire pause when
                // it sees STATE_ENDED.
                _sleepTimer.value = SleepTimerState(mode = mode, remainingMs = 0)
            }
            is SleepTimer.Minutes -> {
                val total = SleepTimerMath.initialMs(mode)
                _sleepTimer.value = SleepTimerState(mode = mode, remainingMs = total)
                sleepTimerJob = viewModelScope.launch {
                    var remaining = total
                    while (remaining > 0) {
                        delay(1_000)
                        remaining -= 1_000
                        _sleepTimer.value = _sleepTimer.value?.copy(remainingMs = remaining)
                    }
                    _sleepTimerFired.value = true
                    _sleepTimer.value = null
                }
            }
        }
    }

    /** Called by the screen's playback-state listener when the player
     *  emits STATE_ENDED. If the active timer is EndOfTrack, fire
     *  pause; otherwise no-op (a track ending under a Minutes timer
     *  doesn't pause early — auto-advance handles next-track). */
    fun onPlayerEnded() {
        if (_sleepTimer.value?.mode == SleepTimer.EndOfTrack) {
            _sleepTimerFired.value = true
            _sleepTimer.value = null
        }
    }

    /** Acknowledge the fired signal so it can edge-trigger again. */
    fun consumeSleepTimerFired() {
        _sleepTimerFired.value = false
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

    private var transcodeSessionId: String? = null
    private var transcodeToken: String? = null
    var hlsOffsetMs: Long = 0L
        private set

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

    fun prepare(itemId: String) {
        viewModelScope.launch {
            try {
                val item = itemRepo.getItem(itemId)
                val file = item.files.firstOrNull()
                if (file == null) {
                    _state.value = PlayerUiState(loading = false, error = "No playable file")
                    return@launch
                }
                val serverUrl = serverPrefs.getServerUrl()?.trimEnd('/').orEmpty()
                val prefs = try { preferencesRepo.get() } catch (_: Exception) { null }
                val mode = PlaybackHelper.decide(file)
                val startMs = item.view_offset_ms

                // Offline-first: if the user has a completed download
                // for this file, play the local copy. Skips even the
                // transcode/remux negotiation — the on-disk file is
                // the original bytes the server has.
                downloads.store.load()
                val downloaded = downloads.store.get(file.id)
                val localFile = downloaded?.takeIf { it.status == "completed" }
                    ?.let { downloads.store.fileFor(it) }
                    ?.takeIf { it.exists() && it.length() > 0 }

                val source = when {
                    localFile != null -> {
                        hlsOffsetMs = 0
                        lastTranscodeRequest = null
                        PlaybackSource.DirectPlay("file://${localFile.absolutePath}", startMs)
                    }
                    mode is PlaybackMode.DirectPlay -> {
                        hlsOffsetMs = 0
                        lastTranscodeRequest = null
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

                val markers = itemRepo.getMarkers(itemId)

                _state.value = PlayerUiState(
                    loading = false,
                    source = source,
                    item = item,
                    audioStreams = file.audio_streams,
                    subtitles = file.subtitle_streams,
                    markers = markers,
                    preferredAudioLang = prefs?.preferred_audio_lang,
                    preferredSubtitleLang = prefs?.preferred_subtitle_lang,
                )

                // Trickplay cues — best-effort. If the server hasn't
                // generated thumbnails yet (status != "done"), the
                // status fetch returns a `not_started` sentinel and
                // we skip the VTT fetch entirely. Local-file playback
                // (offline) also skips since trickplay endpoints need
                // a live server.
                if (localFile == null) {
                    loadTrickplayCues(itemId)
                }

                // Lyrics — only meaningful for tracks. Server 404s
                // for non-tracks anyway; the repo maps that to null.
                // Best-effort, no error surface — overlay just doesn't
                // appear when there are none.
                if (item.type == "track" && localFile == null) {
                    loadLyrics(itemId)
                }

                // Episode + track auto-advance: same parent + index
                // relationship on both sides. The screen branches on
                // item.type to decide whether to surface an overlay
                // (episodes) or chain silently (music tracks).
                if (item.parent_id != null && item.index != null &&
                    (item.type == "episode" || item.type == "track")) {
                    loadNextSibling(item.parent_id, item.index, item.type)
                }

                subscribeRemoteProgress(itemId)
            } catch (e: Exception) {
                val msg = if (e is HttpException && e.code() == 403) "content_restricted"
                else e.message
                _state.value = PlayerUiState(loading = false, error = msg)
            }
        }
    }

    private suspend fun loadNextSibling(parentId: String, currentIndex: Int, type: String) {
        try {
            val children = itemRepo.getChildren(parentId)
            val next = children
                .filter { it.type == type && it.index != null }
                .sortedBy { it.index }
                .firstOrNull { (it.index ?: -1) == currentIndex + 1 }
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
        audioStreamIndex: Int? = null,
    ): PlaybackSource {
        stopActiveTranscode()

        val session = transcodeRepo.start(
            itemId = itemId,
            height = height,
            positionMs = posMs,
            fileId = fileId,
            videoCopy = videoCopy,
            audioStreamIndex = audioStreamIndex,
            supportsHevc = PlaybackHelper.supportsHevc(),
        )

        transcodeSessionId = session.session_id
        transcodeToken = session.token
        hlsOffsetMs = posMs
        lastTranscodeRequest = TranscodeRequest(itemId, fileId, height, videoCopy, serverUrl)

        return PlaybackSource.Hls("$serverUrl${session.playlist_url}", posMs)
    }

    /** Re-issue the active transcode session with a new
     *  audio_stream_index, preserving the current position. Direct-
     *  play swaps tracks via the player's track selector and never
     *  comes through here. */
    fun switchAudioStream(audioStreamIndex: Int, currentPositionMs: Long) {
        val req = lastTranscodeRequest ?: return
        viewModelScope.launch {
            try {
                val source = startTranscode(
                    itemId = req.itemId,
                    height = req.height,
                    posMs = currentPositionMs + hlsOffsetMs,
                    fileId = req.fileId,
                    videoCopy = req.videoCopy,
                    serverUrl = req.serverUrl,
                    audioStreamIndex = audioStreamIndex,
                )
                _state.value = _state.value.copy(source = source)
            } catch (_: Exception) {
                // Best-effort — leave the existing session running.
            }
        }
    }

    /** Fire-and-forget progress publish. Best-effort: server
     *  unreachability shouldn't crash playback, and the next tick
     *  will pick up where this one left off. */
    fun reportProgress(itemId: String, positionMs: Long, durationMs: Long, state: String) {
        if (durationMs <= 0) return
        localProgressMs = positionMs
        viewModelScope.launch {
            try {
                itemRepo.updateProgress(itemId, positionMs, durationMs, state)
            } catch (_: Exception) { }
        }
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

    /** Download the chosen subtitle and attach it to the active file.
     *  The next /items/{id} fetch will see it in the subtitle_streams
     *  list — the player won't auto-pick it, since it'd need a fresh
     *  prepare(); the user toggles it from the existing subtitle picker. */
    fun downloadOnlineSubtitle(itemId: String, candidate: OnlineSubtitle, onDone: () -> Unit) {
        val fileId = _state.value.item?.files?.firstOrNull()?.id ?: return
        viewModelScope.launch {
            try {
                onlineSubtitles.download(itemId, fileId, candidate)
                _onlineSubtitleSearch.value = OnlineSubtitleSearchUi()
                onDone()
            } catch (e: Exception) {
                _onlineSubtitleSearch.value = _onlineSubtitleSearch.value.copy(error = e.message)
            }
        }
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
                    val delta = kotlin.math.abs(ev.position_ms - localProgressMs)
                    if (delta < 3_000L) return@collect
                    _remoteResumeMs.value = ev.position_ms
                }
            } catch (_: Exception) {
                // SSE drop — leave the player running on its own
                // state. A reconnect tier could be added later but
                // would need a backoff to avoid hammering the server.
            }
        }
    }

    fun stopActiveTranscode() {
        val sid = transcodeSessionId ?: return
        val tok = transcodeToken ?: return
        transcodeSessionId = null
        transcodeToken = null
        viewModelScope.launch { transcodeRepo.stop(sid, tok) }
    }

    /** Direct-play URL with a `?token=` carrier. ExoPlayer's
     *  DefaultHttpDataSource bypasses our OkHttp interceptor chain so
     *  a Bearer header isn't an option; the asset-route middleware
     *  (RequiredAllowQueryToken) accepts the token via query string.
     *
     *  Prefer the per-file 24h stream token over the 1h access token
     *  when the server provides it — ExoPlayer can't refresh on a 401
     *  mid-stream, so the longer-lived token keeps a 90-min movie
     *  from dying with ERROR_CODE_IO_BAD_HTTP_STATUS at the 1h mark. */
    private suspend fun buildDirectPlayUrl(
        serverUrl: String,
        streamPath: String,
        streamToken: String?,
    ): String {
        val token = if (!streamToken.isNullOrEmpty()) streamToken else serverPrefs.getAccessToken()
        val base = "$serverUrl$streamPath"
        if (token.isNullOrEmpty()) return base
        val sep = if (streamPath.contains("?")) "&" else "?"
        return "$base${sep}token=$token"
    }

    override fun onCleared() {
        super.onCleared()
        sseJob?.cancel()
        stopActiveTranscode()
    }
}
