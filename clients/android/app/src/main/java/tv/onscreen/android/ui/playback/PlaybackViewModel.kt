package tv.onscreen.android.ui.playback

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.AudioStream
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.model.Marker
import retrofit2.HttpException
import tv.onscreen.android.data.api.apiError
import tv.onscreen.android.data.model.SubtitleStream
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.PreferencesRepository
import tv.onscreen.android.data.repository.TranscodeRepository
import tv.onscreen.android.data.repository.WatchLimitRepository
import javax.inject.Inject

sealed class PlaybackSource {
    data class DirectPlay(val url: String, val startMs: Long) : PlaybackSource()
    /**
     * Server-issued HLS session.
     *
     * @property playlistUrl absolute URL of the M3U8 (already carries
     *   `?token=…`).
     * @property offsetMs absolute content-time position the HLS stream
     *   actually opens at — used for scrubber-time mapping (display
     *   position = player position + offsetMs). Server-reported
     *   `start_offset_sec * 1000` when present (the keyframe-snapped
     *   position the muxer chose, which may be earlier than the
     *   resume position the client asked for); falls back to the
     *   requested resume position when the server didn't return the
     *   field (older builds).
     * @property initialSeekMs in-stream seek (in HLS-stream-relative
     *   ms) to skip silent video at the head of segment 0. Set when
     *   the server returns a non-zero `seg0_audio_gap_sec` — happens
     *   on mid-stream seek with AC3 → AAC re-encode, where the AAC
     *   encoder's first valid frame lands a few seconds after video's
     *   first packet. The player should `exo.seekTo(initialSeekMs)`
     *   on first start so the first thing the user sees coincides
     *   with the first audible frame instead of silent video.
     */
    data class Hls(
        val playlistUrl: String,
        val offsetMs: Long,
        val initialSeekMs: Long = 0L,
    ) : PlaybackSource()
}

data class PlaybackUiState(
    val source: PlaybackSource? = null,
    val item: ItemDetail? = null,
    val audioStreams: List<AudioStream> = emptyList(),
    val subtitles: List<SubtitleStream> = emptyList(),
    val markers: List<Marker> = emptyList(),
    val nextEpisode: ChildItem? = null,
    val preferredAudioLang: String? = null,
    val preferredSubtitleLang: String? = null,
    val error: String? = null,
)

@HiltViewModel
class PlaybackViewModel @Inject constructor(
    private val itemRepo: ItemRepository,
    private val transcodeRepo: TranscodeRepository,
    private val preferencesRepo: PreferencesRepository,
    private val watchLimitRepo: WatchLimitRepository,
    private val serverPrefs: ServerPrefs,
) : ViewModel() {

    private val _uiState = MutableStateFlow(PlaybackUiState())
    val uiState: StateFlow<PlaybackUiState> = _uiState

    private var transcodeSessionId: String? = null
    private var transcodeToken: String? = null
    var hlsOffsetMs: Long = 0L
        private set

    // Cache the inputs needed to re-issue a transcode session when
    // the user picks a different audio track. ExoPlayer's
    // setPreferredAudioLanguage works for direct play (the player
    // sees every track in the container), but a transcoded HLS
    // stream only carries the one audio the server picked at start
    // time — switching languages means a fresh session with a new
    // audio_stream_index. Null when the active source is direct-
    // play (no re-issue needed).
    private var lastTranscodeRequest: TranscodeRequest? = null

    private data class TranscodeRequest(
        val itemId: String,
        val fileId: String,
        val height: Int,
        val videoCopy: Boolean,
        val serverUrl: String,
    )

    // Set while the active source is a direct play, so a fatal ExoPlayer
    // decode/demux error can re-issue the item as a server transcode (the
    // Android analogue of the web player's codec-escalation). Null on the
    // remux / transcode paths — those already run server-side, so a failure
    // there is a different problem. One-shot: cleared on the first fallback
    // so a transcode that also fails surfaces the real error instead of
    // looping.
    private var directPlayContext: DirectPlayContext? = null

    private data class DirectPlayContext(
        val itemId: String,
        val fileId: String,
        val sourceHeight: Int,
        val serverUrl: String,
    )

    fun prepare(itemId: String, startMs: Long, serverUrl: String) {
        viewModelScope.launch {
            try {
                val item = itemRepo.getItem(itemId)
                val file = item.files.firstOrNull()

                if (file == null) {
                    _uiState.value = PlaybackUiState(error = "No playable file")
                    return@launch
                }

                // Parental watch-limit pre-flight — block a restricted user
                // before any stream/transcode starts. Fail-open if the check
                // itself errors; the transcode-start / progress 403 below still
                // catches a cap reached mid-session.
                try {
                    val wl = watchLimitRepo.get()
                    if (!wl.allowed) {
                        _uiState.value = PlaybackUiState(error = "watch_limit:${wl.reason ?: ""}")
                        return@launch
                    }
                } catch (_: Exception) { /* limit lookup failed — fail open */ }

                val prefs = try { preferencesRepo.get() } catch (_: Exception) { null }

                // Server-authoritative play decision (capability profiles). The
                // device's X-Client-Capabilities header (AuthInterceptor) tells the
                // server what it can decode; map the verdict to a PlaybackMode and
                // fall back to the local PlaybackHelper when the server is
                // unreachable. ExoPlayer range-requests, so no faststart refinement.
                val verdict = transcodeRepo.decide(itemId, file.id)
                val mode = run {
                    val resolved = when (verdict) {
                        "directPlay" -> PlaybackMode.DirectPlay
                        "directStream" -> PlaybackMode.Remux
                        "transcode" -> PlaybackMode.Transcode(
                            if ((file.resolution_h ?: 1080) >= 2160) 2160 else 1080
                        )
                        // "unsupported" (Dolby Vision) handled below; null → local fallback.
                        else -> PlaybackHelper.decide(file)
                    }
                    android.util.Log.i(
                        "PlaybackViewModel",
                        "playback decision: server=$verdict -> $resolved (${file.video_codec}/${file.audio_codec})",
                    )
                    resolved
                }

                // Dolby Vision is not supported: the server returns the "unsupported"
                // verdict (DV can't be tonemapped correctly server-side — see
                // docs/dolby-vision.md). Show a clear message rather than a broken
                // transcode. hdr_type covers a failed/absent decision call.
                if (verdict == "unsupported" ||
                        file.hdr_type?.equals("dolby_vision", ignoreCase = true) == true) {
                    _uiState.value = PlaybackUiState(error = "dolby_vision")
                    return@launch
                }

                // Default off; armed only on the direct-play branch below.
                directPlayContext = null

                val source = when (mode) {
                    is PlaybackMode.DirectPlay -> {
                        hlsOffsetMs = 0
                        // Arm the transcode fallback: if ExoPlayer can't
                        // actually decode this source (e.g. an HEVC profile
                        // the device rejects, or a container quirk), the
                        // fragment's onPlayerError re-issues it as a full
                        // server transcode at the source resolution.
                        directPlayContext = DirectPlayContext(
                            itemId, file.id, file.resolution_h ?: 1080, serverUrl,
                        )
                        // Direct play: ExoPlayer's track selector
                        // can swap audio + subtitle tracks by
                        // language, so no transcode-session re-
                        // issue path is needed. Clear the cached
                        // request so a stale one from a previous
                        // play doesn't get reused.
                        lastTranscodeRequest = null
                        // ExoPlayer's DefaultHttpDataSource bypasses
                        // our OkHttp interceptor chain, so it can't
                        // carry Authorization: Bearer on /media/stream
                        // requests. The asset-route middleware accepts
                        // the bearer as a ?token= query param via
                        // RequiredAllowQueryToken. Without this,
                        // direct-play files silently 401 — notably
                        // ALL audio, since PlaybackHelper.decide()
                        // returns DirectPlay for audio-only files
                        // (transcode is never invoked, so the per-
                        // session ?token= that videos rely on
                        // doesn't exist for them).
                        PlaybackSource.DirectPlay(
                            buildDirectPlayUrl(serverUrl, file.stream_url, file.stream_token),
                            startMs,
                        )
                    }
                    is PlaybackMode.Remux -> startTranscode(itemId, 0, startMs, file.id, true, serverUrl)
                    is PlaybackMode.Transcode -> startTranscode(itemId, mode.height, startMs, file.id, false, serverUrl)
                }

                // Markers (intro/credits) are episode-only on the
                // server but the endpoint returns an empty list for
                // other types, so we can call unconditionally.
                val markers = itemRepo.getMarkers(itemId)

                _uiState.value = PlaybackUiState(
                    source = source,
                    item = item,
                    audioStreams = file.audio_streams,
                    subtitles = file.subtitle_streams,
                    markers = markers,
                    preferredAudioLang = prefs?.preferred_audio_lang,
                    preferredSubtitleLang = prefs?.preferred_subtitle_lang,
                )

                // Auto-advance support: episodes within a season,
                // tracks within an album. Both use the same parent +
                // index relationship; PlaybackFragment uses the
                // type to decide whether to surface an Up Next
                // overlay (episodes) or just chain silently (tracks).
                if (item.parent_id != null && item.index != null) {
                    when (item.type) {
                        "episode" -> loadNextSibling(item.parent_id, item.index, "episode")
                        "track" -> loadNextSibling(item.parent_id, item.index, "track")
                    }
                }
            } catch (e: Exception) {
                val msg = when {
                    e is HttpException && e.code() == 403 -> {
                        // 403 covers two distinct gates: the content-rating
                        // ceiling and the parental watch limit. Parse the error
                        // code so each shows the right message.
                        val err = e.apiError()
                        if (err?.code == "PARENTAL_LIMIT") "watch_limit:${err.message ?: ""}"
                        else "content_restricted"
                    }
                    else -> e.message
                }
                _uiState.value = PlaybackUiState(error = msg)
            }
        }
    }

    private suspend fun loadNextSibling(parentId: String, currentIndex: Int, type: String) {
        // Same resolver the MediaSessionService uses on its own
        // STATE_ENDED — keeps the in-fragment Up Next overlay and
        // the service-side autoplay aligned on what comes next, no
        // matter which surface the track ends on.
        val item = _uiState.value.item ?: return
        val next = tv.onscreen.android.playback.NextSiblingResolver(itemRepo)
            .resolve(item.id, type, parentId, currentIndex)
        if (next != null) {
            _uiState.value = _uiState.value.copy(nextEpisode = next)
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
            supportsAv1 = PlaybackHelper.supportsAv1(),
        )

        transcodeSessionId = session.session_id
        transcodeToken = session.token
        // Prefer the server-reported open position (start_offset_sec —
        // keyframe-aligned, may be earlier than what we asked for) over
        // the requested posMs. Without this, the scrubber-time mapping
        // is off by a couple of seconds whenever the input -ss snaps
        // back to a keyframe — visible to the user as "I scrubbed to
        // 0:00 but the video is at 1:58". Falls back to posMs when the
        // server didn't return the field (omitempty / older builds).
        val openOffsetMs = if (session.start_offset_sec > 0.0) {
            (session.start_offset_sec * 1000.0).toLong()
        } else {
            posMs
        }
        // Initial in-stream seek to skip the silent-video gap at seg 0
        // head. Non-zero only after a mid-stream seek with AC3 → AAC
        // re-encode; the player jumps this far in on first start so
        // the first frame the user sees lands together with the first
        // audible audio frame instead of silent video while the AAC
        // encoder warms up. Same omitempty fallback.
        val seg0SkipMs = (session.seg0_audio_gap_sec * 1000.0).toLong()

        hlsOffsetMs = openOffsetMs
        lastTranscodeRequest = TranscodeRequest(itemId, fileId, height, videoCopy, serverUrl)

        return PlaybackSource.Hls(
            playlistUrl = "$serverUrl${session.playlist_url}",
            offsetMs = openOffsetMs,
            initialSeekMs = seg0SkipMs,
        )
    }

    /**
     * Re-issue the active transcode session with a new
     * audio_stream_index, preserving the current playback
     * position. Used by the audio-track picker on HLS playback —
     * direct-play swaps tracks via the ExoPlayer track selector
     * and never needs to come through here.
     *
     * Stops the existing session, starts a fresh one at the same
     * server-side parameters but with the new audio index, then
     * re-emits the source flow so the fragment swaps the player's
     * MediaItem. position_ms is included in the start request so
     * the new session is keyframe-snapped to where the user was.
     */
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
                _uiState.value = _uiState.value.copy(source = source)
            } catch (_: Exception) {
                // Best-effort — leave the existing session running.
            }
        }
    }

    /**
     * Refresh the subtitle track list after an online subtitle download,
     * without restarting playback from the stored resume point.
     *
     * The downloaded subtitle is a new server-side row, so we re-fetch
     * the item to pick it up in `subtitle_streams` and update the picker
     * list + item metadata in place. The source is only re-issued for
     * HLS — a transcoded session bakes the subtitle set into its
     * playlist, so the new track can't appear until we re-issue the
     * session (done here at [currentPositionMs], NOT from the resume
     * point). Direct play keeps its existing source untouched: re-
     * preparing it would restart playback for nothing (the server-side
     * sidecar sub isn't muxed into the direct-play container anyway —
     * full prepare() never surfaced it either).
     */
    fun reloadSubtitles(itemId: String, currentPositionMs: Long) {
        viewModelScope.launch {
            try {
                val item = itemRepo.getItem(itemId)
                val file = item.files.firstOrNull() ?: return@launch
                val req = lastTranscodeRequest
                if (req != null) {
                    // HLS / transcode: re-issue at the current position so
                    // the new subtitle shows up in the session playlist.
                    val source = startTranscode(
                        itemId = req.itemId,
                        height = req.height,
                        posMs = currentPositionMs + hlsOffsetMs,
                        fileId = req.fileId,
                        videoCopy = req.videoCopy,
                        serverUrl = req.serverUrl,
                    )
                    _uiState.value = _uiState.value.copy(
                        source = source,
                        item = item,
                        subtitles = file.subtitle_streams,
                    )
                } else {
                    // Direct play: refresh the list only, leave the source
                    // (and thus the running player) alone.
                    _uiState.value = _uiState.value.copy(
                        item = item,
                        subtitles = file.subtitle_streams,
                    )
                }
            } catch (_: Exception) {
                // Best-effort — the download already succeeded; a failed
                // metadata refresh just means the new track shows up on the
                // next natural reload.
            }
        }
    }

    /**
     * ExoPlayer hit a fatal error on a direct-play source — a codec
     * profile the device can't decode, a malformed container, etc. Re-issue
     * the same item as a full server transcode at the source resolution and
     * re-emit the source so the fragment rebinds the player. This is the
     * Android analogue of the web player's codec-escalation (videoWidth == 0
     * → switchToTranscode).
     *
     * Full re-encode (videoCopy = false), not remux: a direct-play decode
     * failure means the device can't handle the source video codec, so
     * stream-copying it into HLS would just reproduce an undecodable stream.
     *
     * One-shot — directPlayContext is cleared on entry, so if the transcode
     * also fails the fragment shows the real error instead of looping.
     */
    fun fallbackFromDirectPlay(currentPositionMs: Long) {
        val ctx = directPlayContext ?: return
        directPlayContext = null
        viewModelScope.launch {
            try {
                val height = if (ctx.sourceHeight >= 2160) 2160 else 1080
                val source = startTranscode(
                    itemId = ctx.itemId,
                    height = height,
                    posMs = currentPositionMs.coerceAtLeast(0L),
                    fileId = ctx.fileId,
                    videoCopy = false,
                    serverUrl = ctx.serverUrl,
                )
                _uiState.value = _uiState.value.copy(source = source, error = null)
            } catch (e: Exception) {
                _uiState.value = _uiState.value.copy(error = e.message ?: "Playback failed")
            }
        }
    }

    /**
     * Tell the server to tear down the active transcode session.
     *
     * [scope] defaults to [viewModelScope] for the in-session calls
     * (onStop, audio-switch, fallback). The onCleared safety-net passes
     * [appScope] instead: viewModelScope is *already cancelled* by the
     * time onCleared runs, so a DELETE launched on it would be cancelled
     * before the request leaves the device — leaking the server-side
     * ffmpeg process. appScope outlives the ViewModel so the DELETE
     * actually fires.
     */
    fun stopActiveTranscode(scope: kotlinx.coroutines.CoroutineScope = viewModelScope) {
        val sid = transcodeSessionId ?: return
        val tok = transcodeToken ?: return
        transcodeSessionId = null
        transcodeToken = null

        scope.launch {
            transcodeRepo.stop(sid, tok)
        }
    }

    /**
     * Build a direct-play stream URL with the bearer token appended
     * as `?token=`. The server's asset-route middleware
     * (RequiredAllowQueryToken) accepts that as the auth carrier
     * since ExoPlayer's HTTP stack can't attach an Authorization
     * header.
     *
     * Prefer the per-file [streamToken] (24 h, baked into the file
     * response); fall back to the 24 h purpose=asset token — NOT the
     * access token, which the server rejects in a `?token=` URL.
     * ExoPlayer can't refresh on a 401 mid-stream, so either
     * long-lived token keeps a 90-minute movie from dying with
     * ERROR_CODE_IO_BAD_HTTP_STATUS at the 1 h mark.
     */
    private suspend fun buildDirectPlayUrl(serverUrl: String, streamPath: String, streamToken: String?): String {
        val token = if (!streamToken.isNullOrEmpty()) streamToken else serverPrefs.getAssetToken()
        val base = "$serverUrl$streamPath"
        if (token.isNullOrEmpty()) return base
        val sep = if (streamPath.contains("?")) "&" else "?"
        return "$base${sep}token=$token"
    }

    override fun onCleared() {
        super.onCleared()
        // viewModelScope is cancelled by super.onCleared(), so launch the
        // safety-net DELETE on the application-scoped survivor instead —
        // otherwise it's cancelled before the request goes out and the
        // server-side ffmpeg session leaks.
        stopActiveTranscode(appScope)
    }

    companion object {
        /** Application-scoped survivor for fire-and-forget teardown that
         *  must outlive the ViewModel (the onCleared transcode DELETE).
         *  SupervisorJob so one failed teardown doesn't poison the next. */
        private val appScope =
            kotlinx.coroutines.CoroutineScope(
                kotlinx.coroutines.SupervisorJob() + kotlinx.coroutines.Dispatchers.IO,
            )
    }
}
