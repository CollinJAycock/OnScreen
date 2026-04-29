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

                // The "seasons" map name is historical (it was originally
                // show → season → episode). For album / podcast we reuse
                // the same shape: one synthetic parent with the direct
                // children attached. For artist we treat each child album
                // as its own parent group with empty contents — clicks
                // route through Navigator (drilling into the album's
                // own DetailFragment) instead of PlaybackFragment, so the
                // recursive "play first track of first album" path isn't
                // needed at this layer.
                val seasons = when (item.type) {
                    "show" -> buildSeasonMap(itemId)

                    "season", "album", "podcast", "audiobook" -> {
                        // Direct children are playable (episodes /
                        // tracks / podcast episodes / audiobook
                        // chapters). Load them and present as a single
                        // group keyed by a synthetic ChildItem.
                        //
                        // For audiobooks, this returns empty for the
                        // single-file layout (the row has files of its
                        // own; Play hits configurePlayButtons' "play
                        // self" branch) and a chapter list for the
                        // multi-file layout.
                        val children = itemRepo.getChildren(itemId)
                        val parent = ChildItem(
                            id = item.id,
                            title = item.title,
                            type = item.type,
                            index = item.index,
                        )
                        mapOf(parent to children)
                    }

                    "artist" -> {
                        // Children are albums (containers). Render the
                        // grid; clicks should drill, not play. The
                        // Play-on-artist UX is "play first track of
                        // first album" but that's a two-level traversal;
                        // skip for now and rely on the user picking an
                        // album.
                        val albums = itemRepo.getChildren(itemId)
                        val parent = ChildItem(
                            id = item.id,
                            title = item.title,
                            type = "artist",
                            index = item.index,
                        )
                        mapOf(parent to albums)
                    }

                    else -> emptyMap()
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
