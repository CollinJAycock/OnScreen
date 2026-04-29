package tv.onscreen.mobile.ui.player

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
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
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.PreferencesRepository
import tv.onscreen.mobile.data.repository.TranscodeRepository
import javax.inject.Inject

sealed class PlaybackSource {
    data class DirectPlay(val url: String, val startMs: Long) : PlaybackSource()
    data class Hls(val playlistUrl: String, val offsetMs: Long) : PlaybackSource()
}

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
    val error: String? = null,
)

@HiltViewModel
class PlayerViewModel @Inject constructor(
    private val itemRepo: ItemRepository,
    private val transcodeRepo: TranscodeRepository,
    private val preferencesRepo: PreferencesRepository,
    private val serverPrefs: ServerPrefs,
    private val downloads: tv.onscreen.mobile.data.downloads.OnScreenDownloadManager,
) : ViewModel() {

    private val _state = MutableStateFlow(PlayerUiState())
    val state: StateFlow<PlayerUiState> = _state.asStateFlow()

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

                // Episode + track auto-advance: same parent + index
                // relationship on both sides. The screen branches on
                // item.type to decide whether to surface an overlay
                // (episodes) or chain silently (music tracks).
                if (item.parent_id != null && item.index != null &&
                    (item.type == "episode" || item.type == "track")) {
                    loadNextSibling(item.parent_id, item.index, item.type)
                }
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
        viewModelScope.launch {
            try {
                itemRepo.updateProgress(itemId, positionMs, durationMs, state)
            } catch (_: Exception) { }
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
        stopActiveTranscode()
    }
}
