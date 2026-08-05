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
import tv.onscreen.android.data.model.ItemFile
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

/**
 * A subtitle track the player should side-load alongside the media source.
 *
 * Server HLS sessions carry NO text streams — `transcodeStartRequest` has no
 * subtitle field, and the server extracts subtitles as separate `.vtt` files
 * rather than muxing them into the session playlist. So on every transcode /
 * remux path ExoPlayer's track selector sees zero text tracks and subtitle
 * selection silently does nothing. Side-loading the server's WebVTT rendition
 * as an explicit [androidx.media3.common.MediaItem.SubtitleConfiguration] is
 * what actually makes the subtitle picker work there.
 *
 * @property url absolute `/media/subtitles/{fileId}/{absoluteStreamIndex}`
 *   (or `/media/external-subtitles/{id}`) URL, already carrying `?token=`.
 *   Note the ABSOLUTE stream index — that endpoint's convention, unlike the
 *   ffmpeg-facing `audio_stream_index` which is relative.
 */
data class SubtitleTrackSource(
    val url: String,
    val language: String,
    val label: String,
    val forced: Boolean,
    val sdh: Boolean = false,
    /** Stable side-load format id — `sub:emb:{absIndex}` for an embedded
     *  stream, `sub:ext:{id}` for an external file. Stamped onto the
     *  Media3 SubtitleConfiguration so the fragment can select and
     *  identify the track by TrackGroup format id instead of by language
     *  string (which made same-language duplicates unreachable). */
    val trackId: String,
    /** Absolute embedded stream index; null for external files. */
    val embeddedIndex: Int? = null,
)

data class PlaybackUiState(
    val source: PlaybackSource? = null,
    val item: ItemDetail? = null,
    val audioStreams: List<AudioStream> = emptyList(),
    val subtitles: List<SubtitleStream> = emptyList(),
    /** Side-load sources matching [subtitles], in the same order. Empty when
     *  no token is available or the source can render its own text tracks. */
    val subtitleSources: List<SubtitleTrackSource> = emptyList(),
    val markers: List<Marker> = emptyList(),
    val nextEpisode: ChildItem? = null,
    val preferredAudioLang: String? = null,
    val preferredSubtitleLang: String? = null,
    /** When true (and a preferred subtitle language is set), auto-
     *  selection enables only a FORCED subtitle track in that language;
     *  if none exists, subtitles stay off. Mirrors the web client's
     *  forcedOnly arg to pickPreferredSubtitle. */
    val forcedSubtitlesOnly: Boolean = false,
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
        /** The audio track the CURRENT session was started with — a seek
         *  re-issue must carry it forward or the user's language choice
         *  silently resets to the server default. */
        val audioStreamIndex: Int? = null,
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
                    subtitleSources = buildSubtitleSources(serverUrl, file),
                    markers = markers,
                    preferredAudioLang = prefs?.preferred_audio_lang,
                    preferredSubtitleLang = prefs?.preferred_subtitle_lang,
                    forcedSubtitlesOnly = prefs?.forced_subtitles_only ?: false,
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
                _uiState.value = PlaybackUiState(error = playbackErrorMessage(e))
            }
        }
    }

    /** Map a playback-start failure onto the fragment's error sentinels.
     *  403 covers two distinct gates: the content-rating ceiling and the
     *  parental watch limit — parse the error code so each shows the right
     *  message instead of a raw "HTTP 403". */
    private fun playbackErrorMessage(e: Exception): String? = when {
        e is HttpException && e.code() == 403 -> {
            val err = e.apiError()
            if (err?.code == "PARENTAL_LIMIT") "watch_limit:${err.message ?: ""}"
            else "content_restricted"
        }
        else -> e.message
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

    /**
     * Build the side-load list for [file]'s embedded subtitle streams, in the
     * same order as `file.subtitle_streams` so a picker index maps straight
     * across. Returns empty when no token is available (the endpoint is on the
     * asset-token route group and ExoPlayer can't send a Bearer header).
     */
    private suspend fun buildSubtitleSources(serverUrl: String, file: ItemFile): List<SubtitleTrackSource> {
        if (file.subtitle_streams.isEmpty() && file.external_subtitles.isEmpty()) return emptyList()
        val token = file.stream_token?.takeIf { it.isNotEmpty() } ?: serverPrefs.getAssetToken()
        if (token.isNullOrEmpty()) return emptyList()
        val tok = java.net.URLEncoder.encode(token, "UTF-8")
        // Image-based tracks (PGS/VOBSUB/DVB) can't be rendered as WebVTT —
        // the server 415s the extraction and SingleSampleMediaSource's
        // treat-errors-as-EOS swallowed the failure, so the picker offered
        // tracks that silently never displayed. Skip them here; on direct
        // play ExoPlayer renders them natively from the container.
        val embedded = file.subtitle_streams
            .filterNot { isImageBasedSubtitle(it.codec) }
            .map { s ->
                SubtitleTrackSource(
                    // ABSOLUTE stream index here — /media/subtitles/{fileId}/{index}
                    // uses the API convention, NOT the relative one ffmpeg's
                    // -map 0:s:N takes. See internal/transcode/ffmpeg.go:181.
                    url = "$serverUrl/media/subtitles/${file.id}/${s.index}?token=$tok",
                    language = s.language,
                    label = s.title.ifBlank { s.language.ifBlank { "Track ${s.index}" } },
                    forced = s.forced,
                    sdh = s.sdh,
                    trackId = "sub:emb:${s.index}",
                    embeddedIndex = s.index,
                )
            }
        val external = file.external_subtitles.map { e ->
            SubtitleTrackSource(
                url = "$serverUrl${e.url}?token=$tok",
                language = e.language,
                label = (e.title ?: "").ifBlank { e.language.ifBlank { "External" } },
                forced = e.forced,
                sdh = e.sdh,
                trackId = "sub:ext:${e.id}",
                embeddedIndex = null,
            )
        }
        return embedded + external
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
        // Capture the outgoing session but DON'T tear it down yet. Stopping
        // first meant a failed start (server 5xx, network blip, rate limit)
        // left the caller with nothing playing at all — the catch blocks in
        // switchAudioStream / reloadSubtitles claim they "leave the existing
        // session running", which was untrue once the DELETE had already
        // fired. Retire the old session only after the new one is in hand.
        val priorSessionId = transcodeSessionId
        val priorToken = transcodeToken

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
        lastTranscodeRequest = TranscodeRequest(itemId, fileId, height, videoCopy, serverUrl, audioStreamIndex)

        // New session is live — now retire the one it replaces.
        if (priorSessionId != null && priorToken != null && priorSessionId != session.session_id) {
            viewModelScope.launch { transcodeRepo.stop(priorSessionId, priorToken) }
        }

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
    fun switchAudioStream(audioStreamOrdinal: Int, currentPositionMs: Long) {
        val req = lastTranscodeRequest ?: return
        // Range-check the ordinal against the track list. The server consumes
        // this as `-map 0:a:N` (the Nth AUDIO stream), but the API's
        // AudioStream.index is the ABSOLUTE ffprobe stream index — a caller
        // that confuses the two selects the wrong track, or names a stream
        // that doesn't exist and kills the session with no diagnostic. Since
        // video occupies #0:0, an absolute index is always >= 1 and usually
        // lands out of range here, so this converts the silent-wrong-track
        // failure into a visible no-op. See internal/transcode/ffmpeg.go:181.
        val trackCount = _uiState.value.audioStreams.size
        if (audioStreamOrdinal < 0 || (trackCount > 0 && audioStreamOrdinal >= trackCount)) {
            android.util.Log.w(
                "PlaybackViewModel",
                "ignoring audio switch: ordinal $audioStreamOrdinal out of range for " +
                    "$trackCount track(s) — callers must pass the position within " +
                    "audioStreams, not AudioStream.index",
            )
            return
        }
        viewModelScope.launch {
            try {
                val source = startTranscode(
                    itemId = req.itemId,
                    height = req.height,
                    posMs = currentPositionMs + hlsOffsetMs,
                    fileId = req.fileId,
                    videoCopy = req.videoCopy,
                    serverUrl = req.serverUrl,
                    audioStreamIndex = audioStreamOrdinal,
                )
                _uiState.value = _uiState.value.copy(source = source)
            } catch (_: Exception) {
                // Best-effort — leave the existing session running.
            }
        }
    }

    /** Guard against overlapping seek re-issues — Leanback commits one
     *  seek per scrub, but a user can commit again while the first
     *  round-trip is in flight. */
    private var reissueInFlight = false

    /**
     * Re-issue the active transcode session at [contentPositionMs] —
     * the seek path for targets OUTSIDE the current session's transcoded
     * window (before the resume point, or beyond the growing live edge).
     * An in-window seek never comes through here, and direct play never
     * needs to (its window IS the full timeline). Carries forward the
     * height, video-copy mode and audio-track choice of the session it
     * replaces; startTranscode's supersede path retires the old session.
     */
    fun reissueAt(contentPositionMs: Long) {
        val req = lastTranscodeRequest ?: return
        if (reissueInFlight) return
        reissueInFlight = true
        viewModelScope.launch {
            try {
                val dur = _uiState.value.item?.duration_ms ?: Long.MAX_VALUE
                val source = startTranscode(
                    itemId = req.itemId,
                    height = req.height,
                    posMs = contentPositionMs.coerceIn(0L, dur),
                    fileId = req.fileId,
                    videoCopy = req.videoCopy,
                    serverUrl = req.serverUrl,
                    audioStreamIndex = req.audioStreamIndex,
                )
                _uiState.value = _uiState.value.copy(source = source)
            } catch (_: Exception) {
                // Best-effort — leave the existing session running; the bar
                // snaps back to the real position on the next tick.
            } finally {
                reissueInFlight = false
            }
        }
    }

    /**
     * Refresh the subtitle track list after an online subtitle download,
     * without touching the running player.
     *
     * The downloaded subtitle is a new server-side row, so we re-fetch the
     * item to pick it up in `subtitle_streams`, then rebuild the side-load
     * sources so the new track becomes selectable. Playback is never
     * restarted and no transcode session is re-issued — see the body for why
     * the previous re-issue was both disruptive and ineffective.
     *
     * [currentPositionMs] is retained for call-site compatibility; nothing
     * here needs it now that the session is left alone.
     */
    @Suppress("UNUSED_PARAMETER")
    fun reloadSubtitles(itemId: String, currentPositionMs: Long) {
        viewModelScope.launch {
            try {
                val item = itemRepo.getItem(itemId)
                val file = item.files.firstOrNull() ?: return@launch
                // Refresh the track list + side-load sources ONLY — never
                // re-issue the session. The old code restarted the whole
                // transcode on the premise that "a transcoded session bakes
                // the subtitle set into its playlist", which is false: the
                // server maps only video + one audio into an HLS session and
                // emits subtitles as separate .vtt files. So the restart cost
                // the user a playback interruption and a fresh ffmpeg spin-up
                // and still surfaced no new track. The newly-downloaded
                // subtitle now arrives as a side-load source instead, which
                // works on both direct-play and HLS.
                val serverUrl = lastTranscodeRequest?.serverUrl
                    ?: directPlayContext?.serverUrl
                    ?: serverPrefs.getServerUrl().orEmpty()
                _uiState.value = _uiState.value.copy(
                    item = item,
                    subtitles = file.subtitle_streams,
                    subtitleSources = buildSubtitleSources(serverUrl, file),
                )
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
                // Cap by the PANEL: re-requesting the source height on a
                // 1080p stick dead-ended 4K titles instead of getting 1080p.
                val height = (if (ctx.sourceHeight >= 2160) 2160 else 1080)
                    .coerceAtMost(PlaybackHelper.displayHeightCap())
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
                // Route through the same sentinel mapping as prepare(): when
                // the server cuts a direct-play stream for a watch limit, this
                // fallback's re-issue is refused by the transcode-start gate
                // with the SAME 403 — the user must see the watch-limit
                // message, not a raw "HTTP 403".
                _uiState.value = _uiState.value.copy(
                    error = playbackErrorMessage(e) ?: "Playback failed",
                )
            }
        }
    }

    /**
     * Tell the server to tear down the active transcode session.
     *
     * ALWAYS launched on [appScope], never on viewModelScope, because this is
     * fire-and-forget teardown that has to outlive whatever is being torn down.
     *
     * viewModelScope used to be the default for the "in-session" callers
     * (onStop, audio switch, fallback). That looks safe — onDestroyView runs
     * while the ViewModel is still alive — but it loses the DELETE on the exact
     * path that matters most. The fragment's teardown call NULLS the session
     * ids and launches on viewModelScope; onCleared then cancels that scope,
     * usually before the request has left the device, AND its own safety net
     * finds the ids already nulled so it does nothing. The result is a
     * server-side ffmpeg process left running until it idle-times out —
     * precisely the leak the onCleared net was added to prevent.
     *
     * [scope] is retained so tests can inject a scope they control.
     */
    fun stopActiveTranscode(scope: kotlinx.coroutines.CoroutineScope = appScope) {
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
        // Safety net for the case where nothing tore the session down earlier.
        // stopActiveTranscode is appScope-backed by default now, so this
        // survives super.onCleared() cancelling viewModelScope.
        stopActiveTranscode()
    }

    companion object {
        /** Application-scoped survivor for fire-and-forget teardown that
         *  must outlive the ViewModel (the onCleared transcode DELETE).
         *  SupervisorJob so one failed teardown doesn't poison the next. */
        private val appScope =
            kotlinx.coroutines.CoroutineScope(
                kotlinx.coroutines.SupervisorJob() + kotlinx.coroutines.Dispatchers.IO,
            )

        /** Mirrors the server's ocr.IsImageBased — the codecs whose VTT
         *  extraction is refused with 415 IMAGE_SUBTITLE. */
        fun isImageBasedSubtitle(codec: String): Boolean =
            when (codec.trim().lowercase()) {
                "hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "xsub", "pgssub" -> true
                else -> false
            }
    }
}
