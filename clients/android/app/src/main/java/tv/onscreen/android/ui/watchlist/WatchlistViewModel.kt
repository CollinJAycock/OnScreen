package tv.onscreen.android.ui.watchlist

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
import tv.onscreen.android.data.model.WatchlistItem
import tv.onscreen.android.data.repository.WatchlistRepository
import tv.onscreen.android.data.repository.WatchlistRepository.Companion.MAX_LIMIT
import tv.onscreen.android.data.repository.WatchlistRepository.Companion.PAGE_SIZE
import javax.inject.Inject

/** One status row: its items so far and the status's total. */
data class WatchlistRow(
    val status: String,
    val total: Int,
    val items: List<WatchlistItem>,
) {
    val hasMore: Boolean get() = items.size < total
}

data class WatchlistUiState(
    /** Non-empty statuses only, in [WatchlistUi.STATUSES] order. */
    val rows: List<WatchlistRow> = emptyList(),
    val isLoading: Boolean = false,
    /** True once a load has finished (success or failure), so the screen can
     *  tell "still loading" from "loaded and genuinely empty". */
    val loaded: Boolean = false,
    val error: String? = null,
)

sealed interface WatchlistEvent {
    /** A status change from the card menu failed; nothing moved. */
    data class ChangeFailed(val rateLimited: Boolean) : WatchlistEvent
}

@HiltViewModel
class WatchlistViewModel @Inject constructor(
    private val repo: WatchlistRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(WatchlistUiState())
    val uiState: StateFlow<WatchlistUiState> = _uiState

    /** One-shot toasts, kept out of the state (see HomeViewModel.events). */
    private val _events = MutableSharedFlow<WatchlistEvent>(extraBufferCapacity = 4)
    val events: SharedFlow<WatchlistEvent> = _events

    /** Bumped by every [load]; a page fetched under an older generation is
     *  dropped instead of being appended onto the reloaded rows. */
    private var generation = 0
    private val pagingInFlight = mutableSetOf<String>()

    init { load() }

    /**
     * (Re)load the counts and every non-empty row. Called on open and on every
     * return to the screen — the user may have changed a status meanwhile.
     *
     * A refresh keeps each row as deep as it was already paged (at least one
     * page), so coming back from a detail page reached 120 cards into a row
     * still finds that card. Keep-content on failure, like the other screens.
     */
    fun load() {
        val gen = ++generation
        pagingInFlight.clear()
        val before = _uiState.value
        _uiState.value = before.copy(isLoading = true, error = null)
        viewModelScope.launch {
            try {
                val counts = repo.counts()
                val depth = before.rows.associate { it.status to it.items.size }
                val rows = coroutineScope {
                    WatchlistUi.STATUSES
                        .filter { counts.totalFor(it) > 0 }
                        .map { status ->
                            async { fetchRow(status, maxOf(PAGE_SIZE, depth[status] ?: 0)) }
                        }
                        .awaitAll()
                }.filter { it.items.isNotEmpty() }
                if (gen != generation) return@launch
                _uiState.value = WatchlistUiState(rows = rows, loaded = true)
            } catch (e: Exception) {
                if (gen != generation) return@launch
                val prev = _uiState.value
                _uiState.value = if (prev.rows.isNotEmpty()) {
                    prev.copy(isLoading = false, loaded = true, error = null)
                } else {
                    WatchlistUiState(loaded = true, error = e.message ?: "load failed")
                }
            }
        }
    }

    /** First [want] items of [status] (in pages of up to [MAX_LIMIT]). */
    private suspend fun fetchRow(status: String, want: Int): WatchlistRow {
        val items = mutableListOf<WatchlistItem>()
        var total: Int
        do {
            val (page, count) = repo.page(status, minOf(want - items.size, MAX_LIMIT), items.size)
            total = count
            items += page
        } while (page.isNotEmpty() && items.size < want && items.size < total)
        val unique = items.distinctBy { it.id }
        return WatchlistRow(status, maxOf(total, unique.size), unique)
    }

    /** Fetch [status]'s next page as the user nears the end of its row. */
    fun loadMore(status: String) {
        val row = _uiState.value.rows.firstOrNull { it.status == status } ?: return
        if (!row.hasMore || status in pagingInFlight) return
        pagingInFlight += status
        val gen = generation
        val offset = row.items.size
        viewModelScope.launch {
            try {
                val (page, total) = repo.page(status, PAGE_SIZE, offset)
                if (gen != generation) return@launch
                val cur = _uiState.value
                _uiState.value = cur.copy(
                    rows = cur.rows.map { r ->
                        if (r.status != status || r.items.size != offset) {
                            r
                        } else {
                            val seen = r.items.mapTo(HashSet()) { it.id }
                            val added = page.filter { seen.add(it.id) }
                            // An empty page means the list shrank under us:
                            // stop asking for more until the next reload.
                            r.copy(
                                items = r.items + added,
                                total = if (page.isEmpty()) r.items.size else maxOf(total, r.items.size + added.size),
                            )
                        }
                    },
                )
            } catch (_: Exception) {
                // A failed page keeps the row as is; the next selection near
                // the end retries.
            } finally {
                if (gen == generation) pagingInFlight -= status
            }
        }
    }

    /** Move [item] to [status], or take it off the watchlist when [status]
     *  is null; the rows reload on success. */
    fun changeStatus(item: WatchlistItem, status: String?) {
        if (status == item.status) return
        viewModelScope.launch {
            try {
                if (status == null) repo.clearStatus(item.id) else repo.setStatus(item.id, status)
            } catch (e: Exception) {
                _events.tryEmit(
                    WatchlistEvent.ChangeFailed(rateLimited = (e as? retrofit2.HttpException)?.code() == 429),
                )
                return@launch
            }
            load()
        }
    }
}
