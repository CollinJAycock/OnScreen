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
            // Keep-content refresh (the HomeViewModel pattern): mark loading
            // without discarding the current list, and keep it on failure.
            // Blanking here on every onResume destroyed grid focus + scroll
            // and replaced a populated screen with an error on one blip.
            _uiState.value = _uiState.value.copy(isLoading = true, error = null)
            try {
                val channels = repo.getChannels()
                val nowNext = repo.nowNextByChannel()
                val entries = channels.map { ch ->
                    val pair = nowNext[ch.id]
                    ChannelEntry(channel = ch, current = pair?.first, next = pair?.second)
                }
                _uiState.value = LiveTVUiState(channels = entries)
            } catch (e: Exception) {
                val prev = _uiState.value
                _uiState.value = if (prev.channels.isNotEmpty()) {
                    prev.copy(isLoading = false, error = null)
                } else {
                    LiveTVUiState(error = e.message ?: "Failed to load channels")
                }
            }
        }
    }
}
