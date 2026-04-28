package tv.onscreen.android.ui.detail

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.repository.FavoritesRepository
import tv.onscreen.android.data.repository.ItemRepository
import javax.inject.Inject

data class DetailUiState(
    val item: ItemDetail? = null,
    /** For shows: season → episodes. For movies: empty. */
    val seasons: Map<ChildItem, List<ChildItem>> = emptyMap(),
    val isFavorite: Boolean = false,
    val error: String? = null,
)

@HiltViewModel
class DetailViewModel @Inject constructor(
    private val itemRepo: ItemRepository,
    private val favoritesRepo: FavoritesRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(DetailUiState())
    val uiState: StateFlow<DetailUiState> = _uiState

    fun load(itemId: String) {
        viewModelScope.launch {
            try {
                val item = itemRepo.getItem(itemId)

                val seasons = if (item.type == "show") {
                    buildSeasonMap(itemId)
                } else if (item.type == "season") {
                    // Viewing a season directly — load its episodes.
                    val episodes = itemRepo.getChildren(itemId)
                    val seasonChild = ChildItem(
                        id = item.id,
                        title = item.title,
                        type = "season",
                        index = item.index,
                    )
                    mapOf(seasonChild to episodes)
                } else {
                    emptyMap()
                }

                _uiState.value = DetailUiState(
                    item = item,
                    seasons = seasons,
                    isFavorite = item.is_favorite,
                )
            } catch (e: Exception) {
                _uiState.value = DetailUiState(error = e.message)
            }
        }
    }

    /** Toggle the favorite state. Optimistically flips UI, reverts on failure. */
    fun toggleFavorite() {
        val current = _uiState.value
        val item = current.item ?: return
        val wasFavorite = current.isFavorite

        _uiState.value = current.copy(isFavorite = !wasFavorite)

        viewModelScope.launch {
            try {
                if (wasFavorite) favoritesRepo.remove(item.id)
                else favoritesRepo.add(item.id)
            } catch (_: Exception) {
                _uiState.value = _uiState.value.copy(isFavorite = wasFavorite)
            }
        }
    }

    /** Load all seasons, then load episodes for each season in parallel. */
    private suspend fun buildSeasonMap(showId: String): Map<ChildItem, List<ChildItem>> {
        val seasonChildren = itemRepo.getChildren(showId)
            .filter { it.type == "season" }
            .sortedBy { it.index }

        val episodeLists = seasonChildren.map { season ->
            viewModelScope.async {
                try {
                    season to itemRepo.getChildren(season.id).sortedBy { it.index }
                } catch (_: Exception) {
                    season to emptyList()
                }
            }
        }.awaitAll()

        return episodeLists.toMap()
    }
}
