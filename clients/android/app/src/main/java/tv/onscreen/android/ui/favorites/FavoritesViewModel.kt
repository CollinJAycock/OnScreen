package tv.onscreen.android.ui.favorites

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.FavoriteItem
import tv.onscreen.android.data.repository.FavoritesRepository
import javax.inject.Inject

data class FavoritesUiState(
    val items: List<FavoriteItem> = emptyList(),
    val isLoading: Boolean = false,
    val error: String? = null,
)

@HiltViewModel
class FavoritesViewModel @Inject constructor(
    private val repo: FavoritesRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(FavoritesUiState())
    val uiState: StateFlow<FavoritesUiState> = _uiState

    init { load() }

    fun load() {
        viewModelScope.launch {
            // Keep-content refresh (the HomeViewModel pattern): mark loading
            // without discarding the current list, and keep it on failure.
            _uiState.value = _uiState.value.copy(isLoading = true, error = null)
            try {
                _uiState.value = FavoritesUiState(items = repo.list(limit = 200))
            } catch (e: Exception) {
                val prev = _uiState.value
                _uiState.value = if (prev.items.isNotEmpty()) {
                    prev.copy(isLoading = false, error = null)
                } else {
                    FavoritesUiState(error = e.message ?: "load failed")
                }
            }
        }
    }
}
