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
            _uiState.value = RecordingsUiState(isLoading = true)
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
                _uiState.value = RecordingsUiState(error = e.message ?: "Failed to load recordings")
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
