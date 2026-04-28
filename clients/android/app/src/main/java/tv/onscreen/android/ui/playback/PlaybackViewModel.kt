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
import retrofit2.HttpException
import tv.onscreen.android.data.model.SubtitleStream
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
                        PlaybackSource.DirectPlay("$serverUrl${file.stream_url}", startMs)
                    }
                    is PlaybackMode.Remux -> startTranscode(itemId, 0, startMs, file.id, true, serverUrl)
                    is PlaybackMode.Transcode -> startTranscode(itemId, mode.height, startMs, file.id, false, serverUrl)
                }

                _uiState.value = PlaybackUiState(
                    source = source,
                    item = item,
                    audioStreams = file.audio_streams,
                    subtitles = file.subtitle_streams,
                    preferredAudioLang = prefs?.preferred_audio_lang,
                    preferredSubtitleLang = prefs?.preferred_subtitle_lang,
                )

                if (item.type == "episode" && item.parent_id != null && item.index != null) {
                    loadNextEpisode(item.parent_id, item.index)
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

    private suspend fun loadNextEpisode(parentId: String, currentIndex: Int) {
        try {
            val children = itemRepo.getChildren(parentId)
            val next = children
                .filter { it.type == "episode" && it.index != null }
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

    override fun onCleared() {
        super.onCleared()
        stopActiveTranscode()
    }
}
