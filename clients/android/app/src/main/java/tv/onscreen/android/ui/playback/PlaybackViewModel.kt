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
import tv.onscreen.android.data.model.SubtitleStream
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.PreferencesRepository
import tv.onscreen.android.data.repository.TranscodeRepository
import javax.inject.Inject

sealed class PlaybackSource {
    data class DirectPlay(val url: String, val startMs: Long) : PlaybackSource()
    data class Hls(val playlistUrl: String, val offsetMs: Long) : PlaybackSource()
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

    fun prepare(itemId: String, startMs: Long, serverUrl: String) {
        viewModelScope.launch {
            try {
                val item = itemRepo.getItem(itemId)
                val file = item.files.firstOrNull()

                if (file == null) {
                    _uiState.value = PlaybackUiState(error = "No playable file")
                    return@launch
                }

                val prefs = try { preferencesRepo.get() } catch (_: Exception) { null }

                val mode = PlaybackHelper.decide(file)

                val source = when (mode) {
                    is PlaybackMode.DirectPlay -> {
                        hlsOffsetMs = 0
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
                    e is HttpException && e.code() == 403 -> "content_restricted"
                    else -> e.message
                }
                _uiState.value = PlaybackUiState(error = msg)
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
            if (next != null) {
                _uiState.value = _uiState.value.copy(nextEpisode = next)
                return
            }
            // Cross-container auto-advance: when the user finishes
            // the last episode of a season or the last track of an
            // album, fall through to the first child of the *next*
            // sibling container. Plex/Netflix/Plexamp all chain
            // S04E12 → S05E01 by default and "Play All" on an artist
            // needs this to walk the discography. Without it,
            // playback dies at the end of one season / album.
            //
            // For tracks: parent is an album, grandparent the artist;
            //   sibling containers are albums sorted by year.
            // For episodes: parent is a season, grandparent the show;
            //   sibling containers are seasons sorted by index.
            if (type == "track" || type == "episode") {
                val parent = itemRepo.getItem(parentId)
                val grandparentId = parent.parent_id ?: return
                val containerType = if (type == "track") "album" else "season"
                val rawSiblings = itemRepo.getChildren(grandparentId)
                    .filter { it.type == containerType }
                val siblings = if (type == "track") {
                    rawSiblings.sortedWith(
                        compareBy({ it.year ?: Int.MAX_VALUE }, { it.index ?: Int.MAX_VALUE }),
                    )
                } else {
                    rawSiblings.sortedBy { it.index ?: Int.MAX_VALUE }
                }
                val currentIdx = siblings.indexOfFirst { it.id == parentId }
                if (currentIdx < 0) return
                val nextContainer = siblings.getOrNull(currentIdx + 1) ?: return
                val firstChild = itemRepo.getChildren(nextContainer.id)
                    .filter { it.type == type && it.index != null }
                    .sortedBy { it.index }
                    .firstOrNull() ?: return
                _uiState.value = _uiState.value.copy(nextEpisode = firstChild)
            }
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
    ): PlaybackSource.Hls {
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

        val fullUrl = "$serverUrl${session.playlist_url}"
        return PlaybackSource.Hls(fullUrl, posMs)
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

    fun stopActiveTranscode() {
        val sid = transcodeSessionId ?: return
        val tok = transcodeToken ?: return
        transcodeSessionId = null
        transcodeToken = null

        viewModelScope.launch {
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
     * response) over the standard 1 h access token — ExoPlayer can't
     * refresh on a 401 mid-stream, so the longer-lived token is what
     * keeps a 90-minute movie from dying with
     * ERROR_CODE_IO_BAD_HTTP_STATUS at the 1 h mark. Falls back to
     * the access token for older server builds that don't ship the
     * field.
     */
    private suspend fun buildDirectPlayUrl(serverUrl: String, streamPath: String, streamToken: String?): String {
        val token = if (!streamToken.isNullOrEmpty()) streamToken else serverPrefs.getAccessToken()
        val base = "$serverUrl$streamPath"
        if (token.isNullOrEmpty()) return base
        val sep = if (streamPath.contains("?")) "&" else "?"
        return "$base${sep}token=$token"
    }

    override fun onCleared() {
        super.onCleared()
        stopActiveTranscode()
    }
}
