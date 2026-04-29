package tv.onscreen.android.ui.livetv

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.Channel
import tv.onscreen.android.data.model.NowNext
import tv.onscreen.android.data.repository.LiveTVRepository
import javax.inject.Inject

/** A channel decorated with its current + next program for the
 *  channels grid. nowNext is null when the channel has no EPG data
 *  mapped on the server (the UI shows the channel name only). */
data class ChannelEntry(
    val channel: Channel,
    val current: NowNext?,
    val next: NowNext?,
)

data class LiveTVUiState(
    val isLoading: Boolean = false,
    val channels: List<ChannelEntry> = emptyList(),
    val error: String? = null,
)

@HiltViewModel
class LiveTVViewModel @Inject constructor(
    private val repo: LiveTVRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(LiveTVUiState())
    val uiState: StateFlow<LiveTVUiState> = _uiState

    init { load() }

    fun load() {
        viewModelScope.launch {
            _uiState.value = LiveTVUiState(isLoading = true)
            try {
                val channels = repo.getChannels()
                val nowNext = repo.nowNextByChannel()
                val entries = channels.map { ch ->
                    val pair = nowNext[ch.id]
                    ChannelEntry(channel = ch, current = pair?.first, next = pair?.second)
                }
                _uiState.value = LiveTVUiState(channels = entries)
            } catch (e: Exception) {
                _uiState.value = LiveTVUiState(error = e.message ?: "Failed to load channels")
            }
        }
    }
}
