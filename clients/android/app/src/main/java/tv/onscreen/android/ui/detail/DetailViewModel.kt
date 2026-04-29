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

                    "season", "album", "podcast", "audiobook", "book_series" -> {
                        // Direct children are playable (episodes /
                        // tracks / podcast episodes / audiobook
                        // chapters / books). Load them and present as
                        // a single group keyed by a synthetic ChildItem.
                        //
                        // For audiobooks, this returns empty for the
                        // single-file layout (the row has files of its
                        // own; Play hits configurePlayButtons' "play
                        // self" branch) and a chapter list for the
                        // multi-file layout.
                        //
                        // For book_series, children are audiobook rows
                        // ordered by year (release order ≈ reading
                        // order); the list adapter sorts them itself
                        // before render.
                        val children = itemRepo.getChildren(itemId)
                        val parent = ChildItem(
                            id = item.id,
                            title = item.title,
                            type = item.type,
                            index = item.index,
                        )
                        mapOf(parent to children)
                    }

                    "book_author" -> {
                        // Children are book_series rows + standalone
                        // audiobook rows. Render them in a single
                        // group; the card adapter routes clicks via
                        // Navigator (book_series → DetailFragment,
                        // audiobook → DetailFragment leaf path).
                        // Series first, then standalone books, both
                        // sorted within their bucket — gives the same
                        // structure the web client renders without
                        // needing a multi-section UI.
                        val children = itemRepo.getChildren(itemId)
                        val series = children
                            .filter { it.type == "book_series" }
                            .sortedBy { it.title.lowercase() }
                        val books = children
                            .filter { it.type == "audiobook" }
                            .sortedWith(
                                compareByDescending<ChildItem> { it.year ?: -1 }
                                    .thenBy { it.title.lowercase() },
                            )
                        val parent = ChildItem(
                            id = item.id,
                            title = item.title,
                            type = "book_author",
                            index = item.index,
                        )
                        mapOf(parent to (series + books))
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

    /** Resolve the first track of the artist's chronologically-first
     *  album. Used by the Play All button on the artist detail page —
     *  the player's auto-advance then chains through every album. */
    fun resolvePlayAllStart(artistId: String, onResolved: (String?) -> Unit) {
        viewModelScope.launch {
            val firstTrack = runCatching {
                val albums = itemRepo.getChildren(artistId)
                    .filter { it.type == "album" }
                    .sortedWith(compareBy({ it.year ?: Int.MAX_VALUE }, { it.index ?: Int.MAX_VALUE }))
                albums.firstNotNullOfOrNull { album ->
                    itemRepo.getChildren(album.id)
                        .filter { it.type == "track" && it.index != null }
                        .minByOrNull { it.index ?: Int.MAX_VALUE }
                }
            }.getOrNull()
            onResolved(firstTrack?.id)
        }
    }

    /** Pick a random track from any of the artist's albums. Used by
     *  the Shuffle button on the artist detail page. The player's
     *  auto-advance chains through every subsequent album in order —
     *  Plexamp's true "shuffle queue" re-orders every next-track
     *  lookup, but that needs a queue model the player doesn't have
     *  today. */
    fun resolveShuffleStart(artistId: String, onResolved: (String?) -> Unit) {
        viewModelScope.launch {
            val randomTrack = runCatching {
                val albums = itemRepo.getChildren(artistId).filter { it.type == "album" }
                val allTracks = albums.flatMap { album ->
                    runCatching {
                        itemRepo.getChildren(album.id).filter { it.type == "track" }
                    }.getOrDefault(emptyList())
                }
                allTracks.randomOrNull()
            }.getOrNull()
            onResolved(randomTrack?.id)
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
