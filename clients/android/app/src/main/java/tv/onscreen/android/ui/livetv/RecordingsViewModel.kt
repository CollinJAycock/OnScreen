package tv.onscreen.android.ui.livetv

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.Recording
import tv.onscreen.android.data.repository.LiveTVRepository
import javax.inject.Inject

data class RecordingsUiState(
    val isLoading: Boolean = false,
    val items: List<Recording> = emptyList(),
    val error: String? = null,
)

@HiltViewModel
class RecordingsViewModel @Inject constructor(
    private val repo: LiveTVRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(RecordingsUiState())
    val uiState: StateFlow<RecordingsUiState> = _uiState

    init { load() }

    fun load() {
        viewModelScope.launch {
            // Keep-content refresh (the HomeViewModel pattern): mark loading
            // without discarding the current list, and keep it on failure.
            // Blanking here on every onResume destroyed grid focus + scroll
            // and replaced a populated screen with an error on one blip.
            _uiState.value = _uiState.value.copy(isLoading = true, error = null)
            try {
                // Show completed first, then in-flight + scheduled.
                // Failed and cancelled recordings go to the bottom so
                // they're visible (the user might want to retry) but
                // don't crowd out the watchable stuff up top.
                val all = repo.getRecordings()
                val sorted = all.sortedWith(
                    compareByDescending<Recording> { statusRank(it.status) }
                        .thenByDescending { it.starts_at },
                )
                _uiState.value = RecordingsUiState(items = sorted)
            } catch (e: Exception) {
                val prev = _uiState.value
                _uiState.value = if (prev.items.isNotEmpty()) {
                    prev.copy(isLoading = false, error = null)
                } else {
                    RecordingsUiState(error = e.message ?: "Failed to load recordings")
                }
            }
        }
    }

    private fun statusRank(status: String): Int = when (status) {
        "completed" -> 4
        "recording" -> 3
        "scheduled" -> 2
        "failed" -> 1
        else -> 0
    }
}
