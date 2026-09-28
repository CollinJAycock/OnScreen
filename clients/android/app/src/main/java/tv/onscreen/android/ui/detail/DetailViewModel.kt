package tv.onscreen.android.ui.detail

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import retrofit2.HttpException
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.model.UpNext
import tv.onscreen.android.data.model.WatchStateValue
import tv.onscreen.android.data.repository.FavoritesRepository
import tv.onscreen.android.data.repository.ItemRepository
import javax.inject.Inject

data class DetailUiState(
    val item: ItemDetail? = null,
    /** For shows: season → episodes. For movies: empty. */
    val seasons: Map<ChildItem, List<ChildItem>> = emptyMap(),
    val isFavorite: Boolean = false,
    val error: String? = null,
    /** Show / season: what Play starts (GET /items/{id}/up-next). Null for
     *  other types, or when the call failed (older server) — the fragment
     *  then falls back to picking from the loaded episodes itself. */
    val upNext: UpNext? = null,
    /** Movie / episode / other playable video: the caller's watched state
     *  (item watch_state == "watched"); drives the Mark watched toggle. */
    val watched: Boolean = false,
    /** A whole-item mark (leaf toggle or show / season "Mark all") is in
     *  flight — the button is disabled until it settles. */
    val markBusy: Boolean = false,
)

/** One-shot results of the watched-mark actions (toasts). */
sealed interface DetailEvent {
    /** Leaf toggle ([all] = false) or show / season "Mark all" ([all] = true)
     *  succeeded. */
    data class Marked(val watched: Boolean, val all: Boolean) : DetailEvent
    /** A mark failed and was rolled back; [rateLimited] = HTTP 429. */
    data class MarkFailed(val rateLimited: Boolean) : DetailEvent
}

@HiltViewModel
class DetailViewModel @Inject constructor(
    private val itemRepo: ItemRepository,
    private val favoritesRepo: FavoritesRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(DetailUiState())
    val uiState: StateFlow<DetailUiState> = _uiState

    private val _events = MutableSharedFlow<DetailEvent>(extraBufferCapacity = 4)
    val events: SharedFlow<DetailEvent> = _events

    /** Episode ids with a row toggle in flight (a second long-press on the
     *  same row is ignored until the first settles). */
    private val episodeMarkBusy = mutableSetOf<String>()

    /** Bumped each time a leaf toggle settles. A [load] whose read started
     *  before the bump may carry the pre-mark watch_state. */
    private var markSettles = 0

    fun load(itemId: String) {
        val settlesAtStart = markSettles
        viewModelScope.launch {
            try {
                val item = itemRepo.getItem(itemId)

                // Up Next runs alongside the children load for shows and
                // seasons. Best-effort: an older server (no endpoint) or a
                // failure just means no up-next label — the fragment falls
                // back to its own pick and opens the first season.
                val upNextDeferred = if (item.type == "show" || item.type == "season") {
                    async { fetchUpNext(item.id) }
                } else {
                    null
                }

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
                        // Children are albums + standalone music videos
                        // (per the v2.0 music_video work — videos hang
                        // off the artist with no album parent so they
                        // can appear as their own shelf on the artist
                        // page). Render albums in the primary "Albums"
                        // tab; if any music_video children exist,
                        // render them in a secondary "Music Videos"
                        // tab. DetailFragment's tabs surface picks up
                        // every entry in the map automatically — the
                        // tabs were originally show-only but the UI
                        // works for any type with multiple groups.
                        val children = itemRepo.getChildren(itemId)
                        val albums = children.filter { it.type == "album" }
                        val musicVideos = children.filter { it.type == "music_video" }
                        // Anything that isn't an album or music_video
                        // (defensive — future child types should still
                        // show up rather than silently disappear).
                        val other = children.filter { it.type != "album" && it.type != "music_video" }

                        val albumsParent = ChildItem(
                            id = item.id,
                            // "Albums" is the pill label rendered by
                            // DetailFragment when item.type == "artist".
                            // (Shows use season.index → "Season N"; for
                            // artist tabs the title field is the literal
                            // text shown, see the type-conditional pill
                            // text in DetailFragment.configureEpisodes.)
                            title = "Albums",
                            type = "artist",
                            index = 0,
                        )
                        val mvParent = ChildItem(
                            id = item.id + "#music_videos",
                            title = "Music Videos",
                            type = "artist",
                            // Index 1 keeps the Music Videos tab to the
                            // right of Albums in DetailFragment's
                            // index-sorted tab strip.
                            index = 1,
                        )

                        buildMap {
                            put(albumsParent, albums + other)
                            if (musicVideos.isNotEmpty()) {
                                put(mvParent, musicVideos)
                            }
                        }
                    }

                    else -> emptyMap()
                }

                val upNext = upNextDeferred?.await()
                // A mark in flight (or settled since this read started) owns
                // `watched` and `markBusy`: the read may predate it, and
                // resetting markBusy re-enabled the button mid-request.
                val prev = _uiState.value
                val sameItem = prev.item?.id == item.id
                val markBusy = sameItem && prev.markBusy
                val keepWatched = markBusy || (sameItem && markSettles != settlesAtStart)
                _uiState.value = DetailUiState(
                    item = item,
                    seasons = seasons,
                    isFavorite = item.is_favorite,
                    upNext = upNext,
                    watched = if (keepWatched) prev.watched else item.watch_state == WatchStateValue.Watched,
                    markBusy = markBusy,
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

    // ── Manual watch state ───────────────────────────────────────────────────

    /**
     * Movie / episode: flip watched ⇄ unwatched. Optimistic; rolled back on
     * failure. Either way the server drops the resume point, so on success the
     * item's view_offset_ms goes to 0 (the fragment's Resume becomes Play).
     */
    fun toggleWatched() {
        val cur = _uiState.value
        val item = cur.item ?: return
        if (cur.markBusy) return
        val next = !cur.watched
        _uiState.value = cur.copy(watched = next, markBusy = true)
        viewModelScope.launch {
            try {
                itemRepo.setWatched(item.id, next)
                markSettles++
                val now = _uiState.value
                _uiState.value = now.copy(
                    item = now.item?.copy(view_offset_ms = 0),
                    // Re-assert: a load() that landed meanwhile may have
                    // written a pre-mark watch_state.
                    watched = next,
                    markBusy = false,
                )
                _events.tryEmit(DetailEvent.Marked(next, all = false))
            } catch (e: Exception) {
                markSettles++
                _uiState.value = _uiState.value.copy(watched = !next, markBusy = false)
                _events.tryEmit(DetailEvent.MarkFailed(e.isRateLimited()))
            }
        }
    }

    /**
     * Show / season: mark every episode underneath (the server expands it).
     * Then re-read the episode lists and Up Next so the badges, progress bars
     * and Play label reflect the new state. The confirm for "unwatched on a
     * whole show" is the fragment's job.
     */
    fun markAll(watched: Boolean) {
        val cur = _uiState.value
        val item = cur.item ?: return
        if (cur.markBusy) return
        _uiState.value = cur.copy(markBusy = true)
        viewModelScope.launch {
            try {
                itemRepo.setWatched(item.id, watched)
            } catch (e: Exception) {
                _uiState.value = _uiState.value.copy(markBusy = false)
                _events.tryEmit(DetailEvent.MarkFailed(e.isRateLimited()))
                return@launch
            }
            val seasons = refreshSeasons(_uiState.value.seasons) { eps ->
                // Refresh failed for this group: show what the mark did.
                eps.map { it.copy(watched = watched, view_offset_ms = 0) }
            }
            val upNext = fetchUpNext(item.id)
            _uiState.value = _uiState.value.copy(
                seasons = seasons,
                upNext = upNext ?: _uiState.value.upNext,
                markBusy = false,
            )
            _events.tryEmit(DetailEvent.Marked(watched, all = true))
        }
    }

    /**
     * Episode row quick toggle. Optimistic (badge + progress flip at once),
     * rolled back on failure; on success the episode's group and Up Next are
     * re-read so the Play button follows.
     */
    fun toggleEpisodeWatched(episode: ChildItem) {
        if (!episodeMarkBusy.add(episode.id)) return
        val next = !episode.watched
        patchEpisode(episode.id) { it.copy(watched = next, view_offset_ms = 0) }
        viewModelScope.launch {
            try {
                itemRepo.setWatched(episode.id, next)
            } catch (e: Exception) {
                patchEpisode(episode.id) {
                    it.copy(watched = episode.watched, view_offset_ms = episode.view_offset_ms)
                }
                episodeMarkBusy.remove(episode.id)
                _events.tryEmit(DetailEvent.MarkFailed(e.isRateLimited()))
                return@launch
            }
            val state = _uiState.value
            val group = state.seasons.entries.firstOrNull { (_, eps) -> eps.any { it.id == episode.id } }
            val seasons = if (group != null) {
                refreshSeasons(mapOf(group.key to group.value)) { it }
            } else {
                emptyMap()
            }
            val upNext = state.item?.takeIf { it.type == "show" || it.type == "season" }?.let { fetchUpNext(it.id) }
            val now = _uiState.value
            _uiState.value = now.copy(
                seasons = now.seasons.mapValues { (key, eps) -> seasons[key] ?: eps },
                upNext = upNext ?: now.upNext,
            )
            episodeMarkBusy.remove(episode.id)
        }
    }

    private fun patchEpisode(id: String, patch: (ChildItem) -> ChildItem) {
        val cur = _uiState.value
        _uiState.value = cur.copy(
            seasons = cur.seasons.mapValues { (_, eps) ->
                if (eps.none { it.id == id }) eps else eps.map { if (it.id == id) patch(it) else it }
            },
        )
    }

    /** Re-read each group's children, keeping the same keys (and order) so the
     *  fragment's season tabs stay valid. A group whose read fails gets
     *  [onFailure] applied to its old list. */
    private suspend fun refreshSeasons(
        groups: Map<ChildItem, List<ChildItem>>,
        onFailure: (List<ChildItem>) -> List<ChildItem>,
    ): Map<ChildItem, List<ChildItem>> = coroutineScope {
        groups.entries.map { (key, eps) ->
            async {
                key to try {
                    itemRepo.getChildren(key.id).sortedBy { it.index }
                } catch (_: Exception) {
                    onFailure(eps)
                }
            }
        }.awaitAll().toMap()
    }

    private suspend fun fetchUpNext(id: String): UpNext? =
        try {
            itemRepo.getUpNext(id)
        } catch (_: Exception) {
            null
        }

    private fun Exception.isRateLimited(): Boolean = (this as? HttpException)?.code() == 429

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
                        .minWithOrNull(ChildItem.PLAY_ORDER)
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
