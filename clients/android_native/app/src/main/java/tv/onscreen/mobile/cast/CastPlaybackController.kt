package tv.onscreen.mobile.cast

import android.content.Context
import android.util.Log
import com.google.android.gms.cast.MediaSeekOptions
import com.google.android.gms.cast.MediaStatus
import com.google.android.gms.cast.framework.CastContext
import com.google.android.gms.cast.framework.CastSession
import com.google.android.gms.cast.framework.SessionManagerListener
import com.google.android.gms.cast.framework.media.RemoteMediaClient
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import retrofit2.HttpException
import tv.onscreen.mobile.data.api.HeartbeatRefusal
import tv.onscreen.mobile.data.api.StreamLimit
import tv.onscreen.mobile.data.api.apiError
import tv.onscreen.mobile.data.backgroundScope
import tv.onscreen.mobile.data.model.ItemFile
import tv.onscreen.mobile.data.model.TranscodeRequest
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.PlayDecisionResult
import tv.onscreen.mobile.data.repository.ServerCapabilitiesRepository
import tv.onscreen.mobile.data.repository.TranscodeRepository
import tv.onscreen.mobile.playback.LocalProgressTracker
import tv.onscreen.mobile.ui.player.SubtitleTracks
import java.util.concurrent.TimeUnit
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Casts an item to the connected Chromecast (Google's Default Media Receiver)
 * and keeps what it plays in step with the server — any item, not only the
 * files the DMR plays as they are.
 *
 * Start ([cast]): the play decision and, when needed, a transcode session are
 * asked for with the CHROMECAST's capability profile ([ChromecastCaps], chosen
 * by the device's model name), not the phone's. A file the DMR plays as it is
 * with a directPlay verdict is cast directly, as before; anything else is cast
 * as the session's HLS playlist (see [CastHls] for the rules). A 4K-profile
 * load the receiver fails is retried once with the HD profile.
 *
 * While it plays: progress is reported like the phone's own player does
 * ('playing' every 10 s, 'paused' on pause, 'stopped' at the end) from the
 * receiver's state, in CONTENT time — session offset + receiver position — so
 * resume and Now Playing work. A seek outside what the session has produced
 * starts a new session there ([seekTo]). When the cast ends — the media
 * finishes, the user stops it here or on the receiver, or the Cast session
 * closes — the transcode session is deleted.
 *
 * App-lifetime singleton: casting carries on after the player screen closes.
 * All Cast SDK calls run on the main thread ([scope]).
 */
@Singleton
class CastPlaybackController @Inject constructor(
    @ApplicationContext private val context: Context,
    private val transcodeRepo: TranscodeRepository,
    private val itemRepo: ItemRepository,
    private val serverPrefs: ServerPrefs,
    private val serverCapabilities: ServerCapabilitiesRepository,
    private val http: OkHttpClient,
) {

    /** What the phone asks to cast. */
    data class Request(
        val item: CastMediaInfo.Item,
        val file: ItemFile,
        /** Content time to start at. */
        val positionMs: Long,
        /** Full content duration, when known (progress beats send it). */
        val contentDurationMs: Long?,
        val hasVideo: Boolean,
        /** The server's audio-relative audio_stream_index; null = default. */
        val audioStreamIndex: Int? = null,
        /** The phone's selected subtitle (SubtitleTrack.trackId), or null. */
        val subtitleTrackId: String? = null,
        /** An image subtitle stream (absolute index) to burn in, or null. */
        val burnSubtitleStreamIndex: Int? = null,
    )

    data class Notice(val title: String?, val message: String)

    /** A cast that ended, for the player screen to pick the item up again
     *  where the receiver left it. */
    data class Ended(
        val itemId: String,
        val positionMs: Long,
        /** The receiver played it to the end: nothing to pick up. */
        val finished: Boolean = false,
    )

    data class State(
        /** The item being cast (or being started), null when none. */
        val itemId: String? = null,
        val title: String? = null,
        val deviceName: String? = null,
        /** Content time. */
        val positionMs: Long = 0L,
        val durationMs: Long = 0L,
        val playing: Boolean = false,
        val buffering: Boolean = false,
        /** Deciding / starting the session / waiting for the receiver. */
        val starting: Boolean = false,
        /** The receiver has the media (playing, paused or buffering it). */
        val loaded: Boolean = false,
        val notice: Notice? = null,
        val ended: Ended? = null,
    ) {
        val active: Boolean get() = itemId != null
    }

    private val _state = MutableStateFlow(State())
    val state: StateFlow<State> = _state.asStateFlow()

    private val scope = backgroundScope("CastPlayback", Dispatchers.Main.immediate)

    /** The cast under way. */
    private class Active(
        val req: Request,
        val origin: String,
        val profile: ChromecastCaps.Profile,
        val route: CastHls.Route,
        val decision: String,
        /** The client_name its progress goes under ("Chromecast — <name>"). */
        val clientName: String,
        val sessionId: String?,
        val sessionToken: String?,
        val offsetMs: Long,
        val contentId: String,
        val retried: Boolean,
        val generation: Int,
    ) {
        var loaded = false
        var lastContentMs: Long = req.positionMs
        var lastReported: String? = null

        /** A receiver failure was handled (the LOAD's result and the
         *  receiver's IDLE/ERROR status both report one). */
        var failed = false
    }

    private var active: Active? = null
    private var generation = 0
    private var opJob: Job? = null
    private var heartbeat: Job? = null
    private var attachedClient: RemoteMediaClient? = null
    private var listening = false

    // ── public API (main thread) ─────────────────────────────────────────────

    /** True while [itemId] is being cast (or started): the phone's own player
     *  must neither play it nor report a 'stopped' for it — the server drops
     *  every stream session of the item on that report, the cast's included. */
    fun isCasting(itemId: String?): Boolean = itemId != null && _state.value.itemId == itemId

    fun cast(req: Request) {
        opJob?.cancel()
        opJob = scope.launch { castNow(req, profileOverride = null, retried = false) }
    }

    fun togglePause() {
        if (active == null) return
        client()?.togglePlayback()
    }

    /** Seek to content [targetMs]. */
    fun seekTo(targetMs: Long) {
        val a = active ?: return
        val client = client() ?: return
        if (a.route == CastHls.Route.Direct) {
            client.seek(MediaSeekOptions.Builder().setPosition(targetMs.coerceAtLeast(0)).build())
            return
        }
        val plan = CastHls.planSeek(
            targetMs,
            a.offsetMs,
            receiverPositionMs = client.approximateStreamPosition,
            seekableEndMs = client.mediaStatus?.liveSeekableRange?.endTime,
        )
        when (plan) {
            is CastHls.Seek.Receiver ->
                client.seek(MediaSeekOptions.Builder().setPosition(plan.receiverMs).build())
            is CastHls.Seek.NewSession -> {
                _state.update { it.copy(positionMs = plan.positionMs, buffering = true) }
                opJob?.cancel()
                opJob = scope.launch {
                    launchRoute(
                        a.req.copy(positionMs = plan.positionMs), a.origin, a.profile, a.route,
                        retried = a.retried, replacing = a,
                    )
                }
            }
        }
    }

    /** Stop casting: the receiver stops, progress is reported 'stopped' and
     *  the session deleted. The Cast connection stays. */
    fun stop() {
        opJob?.cancel()
        val a = active
        if (a == null) {
            _state.value = State()
            return
        }
        val pos = currentContentMs(a)
        finish(a, pos)
        runCatching { client()?.stop() }
    }

    fun consumeNotice() = _state.update { it.copy(notice = null) }

    fun consumeEnded() = _state.update { it.copy(ended = null) }

    // ── start ────────────────────────────────────────────────────────────────

    private suspend fun castNow(req: Request, profileOverride: ChromecastCaps.Profile?, retried: Boolean) {
        ensureListening()
        val session = castSession()
        if (session == null || !session.isConnected) {
            notice(null, "Pick a Cast device with the Cast button first.")
            return
        }
        val origin = serverPrefs.getServerUrl()?.trimEnd('/').orEmpty()
        if (origin.isEmpty()) return
        val profile = profileOverride ?: ChromecastCaps.forModelName(session.castDevice?.modelName)

        // A different item was cast before: that one ends here.
        active?.takeIf { it.req.item.id != req.item.id }?.let { finish(it, currentContentMs(it)) }

        _state.update {
            State(
                itemId = req.item.id,
                title = req.item.title,
                deviceName = session.castDevice?.friendlyName,
                positionMs = req.positionMs,
                durationMs = req.contentDurationMs ?: 0L,
                starting = true,
                buffering = true,
            )
        }

        val decided = transcodeRepo.decideFor(req.item.id, req.file.id, profile.header)
        if (decided is PlayDecisionResult.TooManyStreams) {
            fail(StreamLimit.TITLE, decided.message)
            return
        }
        val route = CastHls.route(
            decided.verdict,
            directCastable(req.file),
            audioOnly = !req.hasVideo,
            // The receiver plays a file's default audio track only.
            audioTrackSelected = (req.audioStreamIndex ?: 0) > 0,
            burnIn = req.burnSubtitleStreamIndex != null && req.hasVideo,
        )
        Log.i(TAG, "cast decision: model=${session.castDevice?.modelName} server=${decided.verdict} profile=$profile -> $route")
        when (route) {
            is CastHls.Route.Refuse -> when (route.reason) {
                CastHls.Refusal.DOLBY_VISION -> fail(null, "Dolby Vision is not supported")
                CastHls.Refusal.DAMAGED -> fail(
                    tv.onscreen.mobile.ui.player.DAMAGED_TITLE,
                    tv.onscreen.mobile.ui.player.DAMAGED_MESSAGE,
                )
            }
            else -> launchRoute(req, origin, profile, route, retried, replacing = active)
        }
    }

    /** Start [route] for [req] and LOAD it; [replacing] (a seek's or a
     *  retry's predecessor) has its session deleted once the new one is sent. */
    private suspend fun launchRoute(
        req: Request,
        origin: String,
        profile: ChromecastCaps.Profile,
        route: CastHls.Route,
        retried: Boolean,
        replacing: Active?,
    ) {
        val gen = ++generation
        val burning = req.burnSubtitleStreamIndex != null && route is CastHls.Route.Hls
        val clientName = CastHls.clientName(castSession()?.castDevice?.friendlyName)
        val payload: CastMediaInfo.Payload
        val next: Active
        when (route) {
            CastHls.Route.Direct -> {
                val built = CastMediaInfo.build(
                    req.item, castFileOf(req.file), origin, req.positionMs / 1000.0,
                    assetToken = serverPrefs.getAssetToken(),
                )
                if (built == null) {
                    fail(null, "This can't be cast.")
                    return
                }
                // The whole title from 0: the cues need no shift.
                payload = built.copy(textTracks = listOfNotNull(textTrackFor(req, origin, startMs = 0L)))
                next = Active(
                    req, origin, profile, route, CastHls.decisionOf(route, false), clientName,
                    sessionId = null, sessionToken = null, offsetMs = 0L,
                    contentId = payload.contentId, retried = retried, generation = gen,
                )
            }
            is CastHls.Route.Hls -> {
                val videoCopy = route.videoCopy && !burning
                var height = CastHls.transcodeHeight(req.file.resolution_w, req.file.resolution_h, profile)
                val body = TranscodeRequest(
                    file_id = req.file.id,
                    height = height,
                    position_ms = req.positionMs.coerceAtLeast(0),
                    video_copy = videoCopy,
                    audio_stream_index = req.audioStreamIndex,
                    supports_hevc = profile.hevc,
                    burn_subtitle_stream_index = if (burning) req.burnSubtitleStreamIndex else null,
                )
                val session = try {
                    try {
                        transcodeRepo.startFor(req.item.id, body, profile.header)
                    } catch (e: HttpException) {
                        // A server whose TRANSCODE_MAX_HEIGHT is under the 4K
                        // profile's 2160 refuses that height (400): ask again
                        // at 1080, as the web sender does.
                        if (e.code() != 400 || height <= CastHls.FALLBACK_HEIGHT) throw e
                        height = CastHls.FALLBACK_HEIGHT
                        transcodeRepo.startFor(req.item.id, body.copy(height = height), profile.header)
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    Log.w(TAG, "cast transcode start failed", e)
                    val limit = StreamLimit.messageOf(e)
                    if (limit != null) {
                        fail(StreamLimit.TITLE, limit)
                    } else {
                        val msg = (e as? HttpException)?.apiError()?.message?.takeIf { it.isNotBlank() }
                        fail("Couldn't cast", msg ?: "The server couldn't start a stream for the Cast device.")
                    }
                    return
                }
                if (gen != generation) {
                    // Superseded while it started (another cast / seek).
                    session.session_id.takeIf { it.isNotEmpty() }?.let { transcodeRepo.stopDetached(it, session.token) }
                    return
                }
                val offset = CastHls.sessionOffsetMs(session.start_offset_sec, req.positionMs)
                val playlist = CastHls.absolute(origin, session.playlist_url)
                // The media playlist is authoritative (segment_format is a
                // prediction for a re-encode a worker may fall back on); it
                // also holds the LOAD until segment 0 exists.
                val format = probeSegmentFormat(playlist)
                    ?: CastHls.segmentFormatOf(session.segment_format)
                    ?: CastHls.guessSegmentFormat(videoCopy, req.file.video_codec, height, profile)
                if (gen != generation) {
                    session.session_id.takeIf { it.isNotEmpty() }?.let { transcodeRepo.stopDetached(it, session.token) }
                    return
                }
                // start_ms rebases the content-timed cues onto the session's
                // timeline; a burn-in carries the subtitle in the picture.
                val text = if (burning) null else textTrackFor(req, origin, startMs = offset)
                payload = CastMediaInfo.buildHls(
                    item = req.item,
                    fileId = req.file.id,
                    playlistUrl = playlist,
                    segmentFormat = format,
                    hasVideo = req.hasVideo,
                    contentDurationMs = req.contentDurationMs,
                    offsetMs = offset,
                    receiverStartMs = CastHls.receiverStartMs(req.positionMs, offset, session.seg0_audio_gap_sec),
                    sessionId = session.session_id,
                    baseUrl = origin,
                    textTrack = text,
                    assetToken = serverPrefs.getAssetToken(),
                )
                next = Active(
                    req, origin, profile, route, CastHls.decisionOf(route, burning), clientName,
                    sessionId = session.session_id.takeIf { it.isNotEmpty() },
                    sessionToken = session.token,
                    offsetMs = offset, contentId = payload.contentId, retried = retried, generation = gen,
                )
                Log.i(TAG, "cast HLS: session=${session.session_id} offset=$offset format=$format height=$height copy=$videoCopy")
            }
            is CastHls.Route.Refuse -> return
        }
        if (gen != generation) {
            next.sessionId?.let { transcodeRepo.stopDetached(it, next.sessionToken.orEmpty()) }
            return
        }
        val client = castSession()?.remoteMediaClient
        if (client == null) {
            next.sessionId?.let { transcodeRepo.stopDetached(it, next.sessionToken.orEmpty()) }
            fail(null, "The Cast device disconnected.")
            return
        }
        active = next
        attach(client)
        val sent = CastSender.load(context, payload) { ok -> if (!ok) onReceiverFailure(gen) }
        // The session this one replaces (a seek's, a retry's): the server
        // superseded it already; delete it so nothing waits on its reaper.
        replacing?.takeIf { it !== next }?.sessionId?.let {
            transcodeRepo.stopDetached(it, replacing.sessionToken.orEmpty())
        }
        if (!sent) onReceiverFailure(gen)
        ensureHeartbeat()
    }

    /** The receiver couldn't play the LOAD of [gen]: a 4K-profile load is
     *  retried once with the HD profile (decided afresh: a remux of an HEVC
     *  source is no use to an HD Chromecast); anything else ends the cast. */
    private fun onReceiverFailure(gen: Int) {
        val a = active ?: return
        if (a.generation != gen || a.failed) return
        a.failed = true
        val fallback = if (a.retried) null else ChromecastCaps.fallbackAfterFailure(a.profile)
        val pos = currentContentMs(a)
        if (fallback != null) {
            Log.i(TAG, "cast: receiver failed the ${a.profile} load, retrying as $fallback")
            opJob?.cancel()
            opJob = scope.launch {
                castNow(a.req.copy(positionMs = pos), profileOverride = fallback, retried = true)
            }
            return
        }
        finish(a, pos)
        notice("Couldn't cast", "The Cast device couldn't play this.")
    }

    // ── receiver state ───────────────────────────────────────────────────────

    private val clientCallback = object : RemoteMediaClient.Callback() {
        override fun onStatusUpdated() = onStatus()
    }

    private val progressListener = RemoteMediaClient.ProgressListener { progressMs, _ ->
        val a = active
        if (a != null && a.loaded && matches(a)) {
            a.lastContentMs = CastHls.contentMs(a.offsetMs, progressMs)
            _state.update { it.copy(positionMs = a.lastContentMs) }
        }
    }

    private fun attach(client: RemoteMediaClient) {
        if (attachedClient === client) return
        detach()
        client.registerCallback(clientCallback)
        client.addProgressListener(progressListener, 1000L)
        attachedClient = client
    }

    private fun detach() {
        attachedClient?.let {
            it.unregisterCallback(clientCallback)
            it.removeProgressListener(progressListener)
        }
        attachedClient = null
    }

    /** The receiver's media is [a]'s LOAD (not an earlier one it replaced). */
    private fun matches(a: Active): Boolean {
        val cid = attachedClient?.mediaStatus?.mediaInfo?.contentId ?: return true
        return cid == a.contentId
    }

    private fun onStatus() {
        val a = active ?: return
        val client = attachedClient ?: return
        val status = client.mediaStatus ?: return
        if (!matches(a)) return
        when (status.playerState) {
            MediaStatus.PLAYER_STATE_PLAYING -> {
                markLoaded(a)
                _state.update { it.copy(playing = true, buffering = false) }
                if (a.lastReported != "playing") report(a, "playing", currentContentMs(a))
            }
            MediaStatus.PLAYER_STATE_PAUSED -> {
                markLoaded(a)
                _state.update { it.copy(playing = false, buffering = false) }
                if (a.lastReported != "paused") report(a, "paused", currentContentMs(a))
            }
            MediaStatus.PLAYER_STATE_BUFFERING, MediaStatus.PLAYER_STATE_LOADING ->
                _state.update { it.copy(buffering = true) }
            MediaStatus.PLAYER_STATE_IDLE -> when (status.idleReason) {
                MediaStatus.IDLE_REASON_FINISHED -> if (a.loaded) {
                    finish(a, a.req.contentDurationMs?.takeIf { it > 0 } ?: a.lastContentMs, finished = true)
                }
                MediaStatus.IDLE_REASON_ERROR -> onReceiverFailure(a.generation)
                // Stopped on the receiver, from the notification or another
                // sender.
                MediaStatus.IDLE_REASON_CANCELED -> if (a.loaded) finish(a, a.lastContentMs)
                // INTERRUPTED: a new LOAD (ours) replaced it.
                else -> Unit
            }
        }
    }

    private fun markLoaded(a: Active) {
        if (a.loaded) return
        a.loaded = true
        _state.update { it.copy(starting = false, loaded = true) }
    }

    private fun currentContentMs(a: Active): Long {
        val client = attachedClient
        if (a.loaded && client != null && matches(a)) {
            val pos = client.approximateStreamPosition
            if (pos > 0) a.lastContentMs = CastHls.contentMs(a.offsetMs, pos)
        }
        return a.lastContentMs
    }

    // ── progress ─────────────────────────────────────────────────────────────

    private fun ensureHeartbeat() {
        if (heartbeat?.isActive == true) return
        heartbeat = scope.launch {
            while (true) {
                delay(HEARTBEAT_MS)
                val a = active ?: continue
                val s = _state.value
                if (a.loaded && (s.playing || s.buffering)) report(a, "playing", currentContentMs(a))
            }
        }
    }

    private fun report(a: Active, state: String, positionMs: Long) {
        a.lastReported = state
        val itemId = a.req.item.id
        LocalProgressTracker.record(itemId, positionMs)
        scope.launch {
            val duration = a.req.contentDurationMs?.takeIf { it > 0 }
            if (duration == null && !progressWithoutDuration()) return@launch
            try {
                itemRepo.updateProgressFor(a.clientName, itemId, positionMs, duration, state, a.decision)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                val refusal = HeartbeatRefusal.of(state, e) ?: return@launch
                if (active !== a) return@launch
                finish(a, positionMs, reportStopped = false)
                runCatching { client()?.stop() }
                notice(
                    "Casting stopped",
                    when (refusal) {
                        is HeartbeatRefusal.PlaybackStopped -> refusal.message
                        is HeartbeatRefusal.WatchLimit -> "Playback is blocked by a watch limit on this account."
                        HeartbeatRefusal.ContentRevoked -> "This account can no longer play this item."
                    },
                )
            }
        }
    }

    private suspend fun progressWithoutDuration(): Boolean =
        try {
            serverCapabilities.progressWithoutDuration()
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            false
        }

    /** End [a]: 'stopped' at [positionMs] (unless [reportStopped] is false),
     *  its session deleted, the state cleared with an [Ended] for the screen. */
    private fun finish(a: Active, positionMs: Long, reportStopped: Boolean = true, finished: Boolean = false) {
        if (active === a) {
            active = null
            generation++
            detach()
        }
        if (reportStopped) {
            val itemId = a.req.item.id
            LocalProgressTracker.record(itemId, positionMs)
            scope.launch {
                val duration = a.req.contentDurationMs?.takeIf { it > 0 }
                if (duration == null && !progressWithoutDuration()) return@launch
                itemRepo.reportProgressDetachedFor(a.clientName, itemId, positionMs, duration, "stopped", a.decision)
            }
        }
        a.sessionId?.let { transcodeRepo.stopDetached(it, a.sessionToken.orEmpty()) }
        _state.value = State(ended = Ended(a.req.item.id, positionMs, finished), notice = _state.value.notice)
    }

    private fun fail(title: String?, message: String) {
        _state.value = State(notice = Notice(title, message))
    }

    private fun notice(title: String?, message: String) {
        _state.update { it.copy(notice = Notice(title, message)) }
    }

    // ── Cast session ─────────────────────────────────────────────────────────

    private val sessionListener = object : SessionManagerListener<CastSession> {
        override fun onSessionEnding(session: CastSession) {
            // The last moment the receiver's position can be read.
            active?.let { currentContentMs(it) }
        }

        override fun onSessionEnded(session: CastSession, error: Int) {
            opJob?.cancel()
            active?.let { finish(it, it.lastContentMs) }
            if (_state.value.itemId != null) _state.value = State()
        }

        override fun onSessionStarting(session: CastSession) = Unit
        override fun onSessionStarted(session: CastSession, sessionId: String) = Unit
        override fun onSessionStartFailed(session: CastSession, error: Int) = Unit
        override fun onSessionResuming(session: CastSession, sessionId: String) = Unit
        override fun onSessionResumed(session: CastSession, wasSuspended: Boolean) {
            session.remoteMediaClient?.let { if (active != null) attach(it) }
        }
        override fun onSessionResumeFailed(session: CastSession, error: Int) = Unit
        override fun onSessionSuspended(session: CastSession, reason: Int) = Unit
    }

    private fun ensureListening() {
        if (listening) return
        val ctx = castContext() ?: return
        ctx.sessionManager.addSessionManagerListener(sessionListener, CastSession::class.java)
        listening = true
    }

    private fun castContext(): CastContext? =
        try {
            CastContext.getSharedInstance(context)
        } catch (_: Throwable) {
            null
        }

    private fun castSession(): CastSession? = castContext()?.sessionManager?.currentCastSession

    private fun client(): RemoteMediaClient? = attachedClient ?: castSession()?.remoteMediaClient

    // ── helpers ──────────────────────────────────────────────────────────────

    /** The DMR plays the file as it is (today's rule) — never one part of a
     *  stacked movie, which would play alone and stop. */
    private fun directCastable(file: ItemFile): Boolean =
        file.part_index == null && CastMediaInfo.isCastable(castFileOf(file))

    private fun castFileOf(file: ItemFile) = CastMediaInfo.File(
        id = file.id,
        // ItemFile carries no status: files the API lists are active.
        status = "active",
        container = file.container,
        videoCodec = file.video_codec,
        audioCodec = file.audio_codec,
        durationSeconds = file.duration_ms?.let { it / 1000L },
        streamToken = file.stream_token,
    )

    /** The picked text subtitle as a Cast track, its cues rebased onto a
     *  timeline starting at content [startMs]; null when none is picked, it
     *  is image-based, or the file has no stream token to fetch it with. */
    private fun textTrackFor(req: Request, origin: String, startMs: Long): CastHls.TextTrack? {
        val trackId = req.subtitleTrackId ?: return null
        val file = req.file
        val ext = file.external_subtitles.firstOrNull { "sub:ext:${it.id}" == trackId }
        val emb = file.subtitle_streams.firstOrNull { "sub:emb:${it.index}" == trackId }
        if (ext == null && emb == null) return null
        if (emb != null && (emb.image_based ?: SubtitleTracks.isImageBased(emb.codec))) return null
        val url = CastHls.subtitleUrl(origin, file.id, trackId, file.stream_token, startMs) ?: return null
        return CastHls.TextTrack(
            id = CastHls.TEXT_TRACK_ID,
            url = url,
            language = (ext?.language ?: emb?.language).orEmpty().ifBlank { "und" },
            name = ext?.title ?: emb?.title.orEmpty(),
        )
    }

    /** Read the session's playlist (and a master's first variant) for its
     *  segment format — only when the start response didn't say. */
    private suspend fun probeSegmentFormat(playlistUrl: String): CastHls.SegmentFormat? =
        withContext(Dispatchers.IO) {
            try {
                val client = http.newBuilder().callTimeout(PROBE_TIMEOUT_S, TimeUnit.SECONDS).build()
                fun fetch(url: String): String? =
                    client.newCall(okhttp3.Request.Builder().url(url).build()).execute().use { r ->
                        if (r.isSuccessful) r.body?.string() else null
                    }
                var text = fetch(playlistUrl) ?: return@withContext null
                CastHls.firstVariant(text)?.let { v -> text = fetch(CastHls.resolve(playlistUrl, v)) ?: return@withContext null }
                CastHls.inferSegmentFormat(text)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                Log.w(TAG, "cast: playlist probe failed", e)
                null
            }
        }

    private companion object {
        const val TAG = "CastPlayback"
        const val HEARTBEAT_MS = 10_000L
        const val PROBE_TIMEOUT_S = 30L
    }
}

/** For composables, which can't take constructor injection. */
@EntryPoint
@InstallIn(SingletonComponent::class)
interface CastEntryPoint {
    fun castPlaybackController(): CastPlaybackController
}
