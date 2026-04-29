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
            _uiState.value = _uiState.value.copy(nextEpisode = next)
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
    ): PlaybackSource.Hls {
        stopActiveTranscode()

        val session = transcodeRepo.start(
            itemId = itemId,
            height = height,
            positionMs = posMs,
            fileId = fileId,
            videoCopy = videoCopy,
            supportsHevc = PlaybackHelper.supportsHevc(),
        )

        transcodeSessionId = session.session_id
        transcodeToken = session.token
        hlsOffsetMs = posMs

        val fullUrl = "$serverUrl${session.playlist_url}"
        return PlaybackSource.Hls(fullUrl, posMs)
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
