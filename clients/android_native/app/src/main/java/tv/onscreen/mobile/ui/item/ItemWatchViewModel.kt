package tv.onscreen.mobile.ui.item

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import retrofit2.HttpException
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.UpNext
import tv.onscreen.mobile.data.model.WatchStateValue
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.ui.watch.isWatchContainerType
import tv.onscreen.mobile.ui.watch.isWatchLeafType
import javax.inject.Inject

/**
 * Watch-state half of the item detail page, kept out of
 * [ItemDetailViewModel] so the two evolve independently:
 *
 *  - movie / episode / music & home video: the Watched toggle.
 *  - show / season: the up-next primary button ("Resume S3 · E4", …),
 *    "Mark all watched / unwatched", and the season → episode list with a
 *    per-episode watched toggle. A show opens on the season holding the
 *    up-next episode.
 *
 * The screen calls [bind] with each detail it loads (including the reload
 * when the user comes back from the player), so resume points and marks
 * refresh without a manual pull.
 */
@HiltViewModel
class ItemWatchViewModel @Inject constructor(
    private val repo: ItemRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(ItemWatchUi())
    val state: StateFlow<ItemWatchUi> = _state.asStateFlow()

    /** Bind to a freshly-loaded detail. A new item resets and loads; the
     *  same item again (return from the player, a retry) refreshes in place
     *  without blanking the lists. */
    fun bind(detail: ItemDetail) {
        val current = _state.value
        val sameItem = current.itemId == detail.id
        val leafWatched = detail.watch_state == WatchStateValue.WATCHED
        if (!sameItem) {
            _state.value = ItemWatchUi(
                itemId = detail.id,
                itemType = detail.type,
                itemWatched = leafWatched,
                resumeMs = detail.view_offset_ms,
                loadingContainer = isWatchContainerType(detail.type),
            )
            if (isWatchContainerType(detail.type)) loadContainer(detail.id, detail.type)
            return
        }
        // Same item: take the server's leaf state unless a toggle is in
        // flight (its optimistic value is newer than this read).
        if (!current.markBusy) {
            _state.value = current.copy(itemWatched = leafWatched, resumeMs = detail.view_offset_ms)
        }
        if (isWatchContainerType(detail.type)) {
            if (current.seasons.isEmpty() && current.episodes.isEmpty()) {
                loadContainer(detail.id, detail.type)
            } else {
                viewModelScope.launch { refreshAfterMark(seasonIds = null) }
            }
        }
    }

    /** Re-run the container load after a failure. */
    fun retry() {
        val s = _state.value
        val id = s.itemId ?: return
        if (isWatchContainerType(s.itemType)) loadContainer(id, s.itemType)
    }

    private fun loadContainer(itemId: String, type: String) {
        viewModelScope.launch {
            _state.value = _state.value.copy(loadingContainer = true, containerError = null)
            try {
                val (children, upNext) = coroutineScope {
                    val kids = async { repo.getChildren(itemId) }
                    val un = async { fetchUpNext(itemId) }
                    kids.await() to un.await()
                }
                if (_state.value.itemId != itemId) return@launch
                val seasons = if (type == "show") {
                    children.filter { it.type == "season" }.sortedBy { it.index ?: Int.MAX_VALUE }
                } else {
                    emptyList()
                }
                if (seasons.isNotEmpty()) {
                    // Open the season holding the up-next episode, falling
                    // back to the first one.
                    val selected = seasons.firstOrNull { it.id == upNext?.episode?.season_id } ?: seasons.first()
                    _state.value = _state.value.copy(
                        loadingContainer = false,
                        upNext = upNext,
                        upNextLoaded = true,
                        seasons = seasons,
                        selectedSeasonId = selected.id,
                    )
                    loadEpisodes(selected.id)
                } else {
                    // A season — or a flat show whose episodes sit directly
                    // under it — lists its own episodes.
                    _state.value = _state.value.copy(
                        loadingContainer = false,
                        upNext = upNext,
                        upNextLoaded = true,
                        seasons = emptyList(),
                        selectedSeasonId = itemId,
                        episodes = mapOf(itemId to episodesOf(children)),
                    )
                }
            } catch (e: Exception) {
                if (_state.value.itemId != itemId) return@launch
                _state.value = _state.value.copy(
                    loadingContainer = false,
                    containerError = e.message ?: "Couldn't load episodes",
                )
            }
        }
    }

    /** Switch the show page to another season, loading it on first open. */
    fun selectSeason(seasonId: String) {
        val s = _state.value
        if (s.seasons.none { it.id == seasonId }) return
        _state.value = s.copy(selectedSeasonId = seasonId)
        if (!s.episodes.containsKey(seasonId)) loadEpisodes(seasonId)
    }

    private fun loadEpisodes(seasonId: String) {
        viewModelScope.launch {
            _state.value = _state.value.copy(loadingEpisodes = _state.value.loadingEpisodes + seasonId)
            try {
                val eps = episodesOf(repo.getChildren(seasonId))
                _state.value = _state.value.copy(episodes = _state.value.episodes + (seasonId to eps))
            } catch (_: Exception) {
                // Leave the season unloaded; selecting it again retries.
            } finally {
                _state.value = _state.value.copy(loadingEpisodes = _state.value.loadingEpisodes - seasonId)
            }
        }
    }

    /** Movie / episode: flip watched ⇄ unwatched. Optimistic; rolled back
     *  with a message on failure. Either way the resume point goes away. */
    fun toggleItemWatched() {
        val s = _state.value
        val id = s.itemId ?: return
        if (s.markBusy || !isWatchLeafType(s.itemType)) return
        val next = !s.itemWatched
        _state.value = s.copy(itemWatched = next, resumeMs = 0, markBusy = true)
        viewModelScope.launch {
            try {
                repo.setWatched(id, next)
                if (_state.value.itemId != id) return@launch
                _state.value = _state.value.copy(
                    markBusy = false,
                    message = if (next) "Marked as watched" else "Marked as unwatched",
                )
            } catch (e: Exception) {
                if (_state.value.itemId != id) return@launch
                _state.value = _state.value.copy(
                    itemWatched = !next,
                    resumeMs = s.resumeMs,
                    markBusy = false,
                    message = markErrorMessage(e),
                )
            }
        }
    }

    /** Show / season: mark every episode underneath. The confirm for a
     *  whole-show "unwatched" lives in the screen. */
    fun markAll(watched: Boolean) {
        val s = _state.value
        val id = s.itemId ?: return
        if (s.markBusy || !isWatchContainerType(s.itemType)) return
        _state.value = s.copy(markBusy = true)
        viewModelScope.launch {
            try {
                repo.setWatched(id, watched)
                refreshAfterMark(seasonIds = null)
                if (_state.value.itemId != id) return@launch
                _state.value = _state.value.copy(
                    markBusy = false,
                    message = if (watched) "Marked all as watched" else "Marked all as unwatched",
                )
            } catch (e: Exception) {
                if (_state.value.itemId != id) return@launch
                _state.value = _state.value.copy(markBusy = false, message = markErrorMessage(e))
            }
        }
    }

    /** Show page: mark one season's episodes (the header's "Mark all"
     *  covers the whole show). Refreshes that season + up-next after. */
    fun markSeason(seasonId: String, watched: Boolean) {
        val s = _state.value
        if (s.markBusy || s.seasons.none { it.id == seasonId }) return
        _state.value = s.copy(markBusy = true)
        viewModelScope.launch {
            try {
                repo.setWatched(seasonId, watched)
                refreshAfterMark(seasonIds = listOf(seasonId))
                _state.value = _state.value.copy(
                    markBusy = false,
                    message = if (watched) "Marked season as watched" else "Marked season as unwatched",
                )
            } catch (e: Exception) {
                _state.value = _state.value.copy(markBusy = false, message = markErrorMessage(e))
            }
        }
    }

    /** Episode row quick toggle — optimistic, rolled back on failure; the
     *  season's list and the up-next button refresh after a success. */
    fun toggleEpisodeWatched(episode: ChildItem) {
        val s = _state.value
        if (episode.id in s.episodeBusy) return
        val seasonId = s.episodes.entries.firstOrNull { (_, eps) -> eps.any { it.id == episode.id } }?.key ?: return
        val next = !episode.watched
        _state.value = s.copy(
            episodeBusy = s.episodeBusy + episode.id,
            episodes = s.episodes.patch(seasonId, episode.id) { it.copy(watched = next, view_offset_ms = 0) },
        )
        viewModelScope.launch {
            try {
                repo.setWatched(episode.id, next)
                refreshAfterMark(seasonIds = listOf(seasonId))
            } catch (e: Exception) {
                _state.value = _state.value.copy(
                    episodes = _state.value.episodes.patch(seasonId, episode.id) {
                        it.copy(watched = episode.watched, view_offset_ms = episode.view_offset_ms)
                    },
                    message = markErrorMessage(e),
                )
            } finally {
                _state.value = _state.value.copy(episodeBusy = _state.value.episodeBusy - episode.id)
            }
        }
    }

    /** The screen showed [ItemWatchUi.message]; clear it. */
    fun consumeMessage() {
        _state.value = _state.value.copy(message = null)
    }

    /** Re-read the up-next state and the episode lists ([seasonIds], or
     *  every loaded one) after a mark. Best-effort: a failed re-read keeps
     *  the optimistic / previous values. */
    private suspend fun refreshAfterMark(seasonIds: List<String>?) {
        val id = _state.value.itemId ?: return
        if (!isWatchContainerType(_state.value.itemType)) return
        val ids = seasonIds ?: _state.value.episodes.keys.toList()
        coroutineScope {
            val un = async { fetchUpNext(id) }
            val lists = ids.map { sid ->
                async { sid to runCatching { episodesOf(repo.getChildren(sid)) }.getOrNull() }
            }
            val upNext = un.await()
            val fresh = lists.map { it.await() }
            if (_state.value.itemId != id) return@coroutineScope
            var episodes = _state.value.episodes
            fresh.forEach { (sid, eps) -> if (eps != null) episodes = episodes + (sid to eps) }
            _state.value = _state.value.copy(
                upNext = upNext ?: _state.value.upNext,
                episodes = episodes,
            )
        }
    }

    /** Best-effort: a failure (or a pre-v2.5 server's 404) is "no up-next"
     *  and the screen falls back to a plain Play. */
    private suspend fun fetchUpNext(itemId: String): UpNext? =
        try { repo.getUpNext(itemId) } catch (_: Exception) { null }

    private fun episodesOf(children: List<ChildItem>): List<ChildItem> =
        children.filter { it.type == "episode" }.sortedBy { it.index ?: Int.MAX_VALUE }

    private fun Map<String, List<ChildItem>>.patch(
        seasonId: String,
        episodeId: String,
        change: (ChildItem) -> ChildItem,
    ): Map<String, List<ChildItem>> {
        val eps = this[seasonId] ?: return this
        return this + (seasonId to eps.map { if (it.id == episodeId) change(it) else it })
    }
}

/** User-facing text for a failed mark. 429 = the per-user mark limit. */
internal fun markErrorMessage(e: Exception): String =
    if (e is HttpException && e.code() == 429) {
        "Too many changes at once — try again in a minute"
    } else {
        "Couldn't update watched state"
    }

data class ItemWatchUi(
    val itemId: String? = null,
    val itemType: String = "",
    /** Movie / episode page: is it watched (the toggle's state). */
    val itemWatched: Boolean = false,
    /** The item's resume point (detail view_offset_ms) — what the Play
     *  button's "Resume from …" shows. A mark clears it, as the server does. */
    val resumeMs: Long = 0,
    /** Show / season: what the primary button plays. Null while loading or
     *  when the server has no up-next (older build / failure). */
    val upNext: UpNext? = null,
    val upNextLoaded: Boolean = false,
    /** Show only: its seasons in order (empty for a season page or a flat
     *  show, whose episodes key under the item's own id). */
    val seasons: List<ChildItem> = emptyList(),
    val selectedSeasonId: String? = null,
    /** Loaded episode lists, by season id. */
    val episodes: Map<String, List<ChildItem>> = emptyMap(),
    val loadingContainer: Boolean = false,
    val loadingEpisodes: Set<String> = emptySet(),
    val containerError: String? = null,
    /** A whole-item mark (toggle / mark all) is in flight. */
    val markBusy: Boolean = false,
    /** Episodes with a toggle in flight. */
    val episodeBusy: Set<String> = emptySet(),
    /** One-shot user message (success or failure of a mark). */
    val message: String? = null,
) {
    val selectedEpisodes: List<ChildItem>
        get() = selectedSeasonId?.let { episodes[it] }.orEmpty()

    /** Position of the selected season in the chip row, or -1 (season page /
     *  flat show / not loaded). The row scrolls it into view. */
    val selectedSeasonIndex: Int
        get() = seasons.indexOfFirst { it.id == selectedSeasonId }
}
