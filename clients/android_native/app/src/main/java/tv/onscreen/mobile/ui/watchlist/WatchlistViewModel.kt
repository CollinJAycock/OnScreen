package tv.onscreen.mobile.ui.watchlist

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import retrofit2.HttpException
import tv.onscreen.mobile.data.model.WatchStatus
import tv.onscreen.mobile.data.model.WatchlistCounts
import tv.onscreen.mobile.data.model.WatchlistItem
import tv.onscreen.mobile.data.model.WatchlistSort
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.WatchlistRepository
import javax.inject.Inject

/**
 * The Watchlist screen: one status tab at a time, an optional type filter
 * and a sort, paged 50 at a time. Tab badges and the type chips come from
 * `/watchlist/counts`; both are optional — the list works without them.
 */
@HiltViewModel
class WatchlistViewModel @Inject constructor(
    private val repo: WatchlistRepository,
    private val itemRepo: ItemRepository,
    private val prefs: ServerPrefs,
) : ViewModel() {

    private val _state = MutableStateFlow(WatchlistUi())
    val state: StateFlow<WatchlistUi> = _state.asStateFlow()

    /** Bumped by every first-page fetch: a page that lands after the query
     *  changed (or a refresh started) is dropped. */
    private var generation = 0

    /** Bumped by every counts fetch and every local count adjustment, so a
     *  counts response that left before a status change can't undo it. */
    private var countsGeneration = 0

    private var started = false
    private var quietRefresh: Job? = null

    /** Screen resumed. The first call is the initial load; later ones (back
     *  from a detail page, where the status may have changed) re-pull
     *  quietly so the grid keeps its scroll position. */
    fun onResume() {
        if (!started) {
            started = true
            loadCounts()
            fetchFirstPage(keepItems = false)
        } else {
            refreshQuietly()
        }
    }

    /** Pull-to-refresh: reload page 0 + counts, keeping the grid visible
     *  under the indicator. */
    fun refresh() {
        loadCounts()
        fetchFirstPage(keepItems = true)
    }

    fun selectStatus(status: WatchStatus) {
        val s = _state.value
        if (s.status == status) return
        // A type the new tab doesn't have would show an empty grid.
        val keepType = s.type != null && (s.counts?.statuses?.get(status.wire)?.types?.get(s.type) ?: 0) > 0
        _state.value = s.copy(
            status = status,
            type = if (keepType) s.type else null,
            queryVersion = s.queryVersion + 1,
        )
        fetchFirstPage(keepItems = false)
    }

    /** [type] null = every type. */
    fun selectType(type: String?) {
        val s = _state.value
        if (s.type == type) return
        _state.value = s.copy(type = type, queryVersion = s.queryVersion + 1)
        fetchFirstPage(keepItems = false)
    }

    fun selectSort(sort: WatchlistSort) {
        val s = _state.value
        if (s.sort == sort) return
        _state.value = s.copy(sort = sort, queryVersion = s.queryVersion + 1)
        fetchFirstPage(keepItems = false)
    }

    private fun loadCounts() {
        val gen = ++countsGeneration
        viewModelScope.launch {
            try {
                val counts = repo.counts()
                if (gen != countsGeneration) return@launch
                _state.value = _state.value.copy(counts = counts)
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                // Badges and the type filter are optional: keep what's shown.
            }
        }
    }

    private fun fetchFirstPage(keepItems: Boolean) {
        val gen = ++generation
        val q = _state.value
        viewModelScope.launch {
            _state.value = _state.value.copy(
                loading = true,
                loadingMore = false,
                loadMoreFailed = false,
                error = null,
                items = if (keepItems) _state.value.items else emptyList(),
                total = if (keepItems) _state.value.total else 0,
            )
            try {
                val (items, total) = repo.list(q.status, q.type, q.sort, limit = PAGE_SIZE, offset = 0)
                val serverUrl = prefs.getServerUrl().orEmpty()
                if (gen != generation) return@launch
                _state.value = _state.value.copy(
                    loading = false,
                    items = items.distinctBy { it.id },
                    total = total,
                    serverUrl = serverUrl,
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                if (gen != generation) return@launch
                _state.value = _state.value.copy(loading = false, error = e.message ?: "Couldn't load your watchlist")
            }
        }
    }

    /** Append the next page as the grid nears its end. No-ops while a page
     *  is loading or everything is loaded. */
    fun loadMore() {
        val s = _state.value
        if (s.loading || s.loadingMore || s.items.size >= s.total) return
        val gen = generation
        viewModelScope.launch {
            _state.value = _state.value.copy(loadingMore = true, loadMoreFailed = false)
            try {
                val (more, total) = repo.list(s.status, s.type, s.sort, limit = PAGE_SIZE, offset = s.items.size)
                if (gen != generation) return@launch
                val merged = (_state.value.items + more).distinctBy { it.id }
                _state.value = _state.value.copy(
                    loadingMore = false,
                    items = merged,
                    // A page that adds nothing new means the list shrank (or
                    // pages overlapped): stop paging instead of refetching
                    // the same offset forever.
                    total = if (merged.size == s.items.size) merged.size else total,
                )
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                if (gen != generation) return@launch
                // Re-arm the screen's load-more effect (see LibraryUi).
                _state.value = _state.value.copy(loadingMore = false, loadMoreFailed = true)
            }
        }
    }

    /**
     * Back on the screen: re-pull the loaded window under the current query,
     * page by page, so a status changed on the detail page is reflected
     * without losing the scroll position. Quiet — no indicator, and a failure
     * keeps the grid as it is. Skipped while any load runs.
     */
    private fun refreshQuietly() {
        val s = _state.value
        if (s.loading || s.loadingMore || s.busyId != null || quietRefresh?.isActive == true) return
        loadCounts()
        val gen = generation
        val window = maxOf(s.items.size, PAGE_SIZE)
        quietRefresh = viewModelScope.launch {
            try {
                val fresh = mutableListOf<WatchlistItem>()
                var offset = 0
                var total: Int
                do {
                    val (page, t) = repo.list(s.status, s.type, s.sort, limit = PAGE_SIZE, offset = offset)
                    fresh += page
                    offset += page.size
                    total = t
                } while (page.isNotEmpty() && offset < window && offset < total)
                if (gen != generation) return@launch
                val merged = fresh.distinctBy { it.id }
                _state.value = _state.value.copy(
                    items = merged,
                    total = maxOf(total, merged.size),
                    error = null,
                )
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                // Quiet: keep what's on screen.
            }
        }
    }

    /** "Move to…" ([next] non-null) or "Remove from watchlist" ([next] null)
     *  from a card. The card leaves the tab once the server confirms. One
     *  change at a time. */
    fun changeStatus(item: WatchlistItem, next: WatchStatus?) {
        val s = _state.value
        if (s.busyId != null) return
        if (next != null && next.wire == item.status) return
        _state.value = s.copy(busyId = item.id)
        // A quiet re-pull that left before this change could land after it
        // and put the card back.
        quietRefresh?.cancel()
        viewModelScope.launch {
            try {
                if (next != null) itemRepo.setWatchStatus(item.id, next) else itemRepo.clearWatchStatus(item.id)
                countsGeneration++ // a counts fetch in flight predates this change
                val cur = _state.value
                val r = applyStatusChange(cur.items, cur.counts, item, next)
                val total = if (r.removed) maxOf(0, cur.total - 1) else cur.total
                _state.value = cur.copy(
                    items = r.items,
                    counts = r.counts,
                    total = total,
                    busyId = null,
                    message = if (next != null) {
                        "Moved \"${item.title}\" to ${statusLabel(next)}"
                    } else {
                        "Removed \"${item.title}\" from your watchlist"
                    },
                )
                // Removing cards emptied the screen while more remain: pull
                // the next page forward.
                if (r.items.isEmpty() && total > 0) fetchFirstPage(keepItems = false)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                _state.value = _state.value.copy(busyId = null, message = statusErrorMessage(e))
            }
        }
    }

    fun consumeMessage() {
        _state.value = _state.value.copy(message = null)
    }

    internal companion object {
        const val PAGE_SIZE = 50
    }
}

internal fun statusErrorMessage(e: Exception): String = when {
    e is HttpException && e.code() == 429 -> "Too many changes at once — try again in a minute"
    else -> "Couldn't update the status"
}

data class WatchlistUi(
    val status: WatchStatus = WatchStatus.PLAN_TO_WATCH,
    /** Type filter; null = every type. */
    val type: String? = null,
    val sort: WatchlistSort = WatchlistSort.RECENT,
    /** Starts true: the first fetch is driven by the screen's ON_RESUME, so
     *  the first frame is the spinner rather than a flash of the empty state. */
    val loading: Boolean = true,
    val loadingMore: Boolean = false,
    /** Re-trigger signal for the screen's load-more effect; see LibraryUi. */
    val loadMoreFailed: Boolean = false,
    val items: List<WatchlistItem> = emptyList(),
    val total: Int = 0,
    /** Null until (or unless) /watchlist/counts loads. */
    val counts: WatchlistCounts? = null,
    val serverUrl: String = "",
    val error: String? = null,
    /** The card whose status change is in flight. */
    val busyId: String? = null,
    /** One-shot snackbar text. */
    val message: String? = null,
    /** Bumped when the query (tab / type / sort) changes, so the screen
     *  scrolls back to the top; refreshes keep the position. */
    val queryVersion: Int = 0,
) {
    /** The type chips for the current tab. */
    val typeChoices: List<TypeOption> get() = typeOptions(counts, status)

    /** Show the type chips when there's a choice, or a filter to clear. */
    val showTypeFilter: Boolean get() = typeChoices.size > 1 || type != null
}
