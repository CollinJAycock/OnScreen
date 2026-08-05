package tv.onscreen.android.ui.history

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.HistoryItem
import tv.onscreen.android.data.repository.HistoryRepository
import javax.inject.Inject

data class HistoryUiState(
    val items: List<HistoryItem> = emptyList(),
    val isLoading: Boolean = false,
    val error: String? = null,
)

@HiltViewModel
class HistoryViewModel @Inject constructor(
    private val repo: HistoryRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(HistoryUiState())
    val uiState: StateFlow<HistoryUiState> = _uiState

    init { load() }

    fun load() {
        viewModelScope.launch {
            // Keep-content refresh (the HomeViewModel pattern): mark loading
            // without discarding the current list, and keep it on failure.
            _uiState.value = _uiState.value.copy(isLoading = true, error = null)
            try {
                _uiState.value = HistoryUiState(items = repo.list(limit = 100))
            } catch (e: Exception) {
                val prev = _uiState.value
                _uiState.value = if (prev.items.isNotEmpty()) {
                    prev.copy(isLoading = false, error = null)
                } else {
                    HistoryUiState(error = e.message ?: "load failed")
                }
            }
        }
    }
}
