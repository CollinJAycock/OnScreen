package tv.onscreen.android.ui.browse

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.MediaItem
import tv.onscreen.android.data.repository.LibraryRepository
import tv.onscreen.android.ui.common.WatchStateUi
import javax.inject.Inject

data class LibrarySort(val sort: String, val sortDir: String) {
    companion object {
        val DEFAULT = LibrarySort("title", "asc")

        /** Per-library-type sort default. Most types want title-ASC
         *  (browse alphabetically). Home video + photo + DVR are
         *  date-driven content where "what I shot/recorded most
         *  recently" is the natural top-of-grid — `created_at` DESC
         *  is the closest the server's sort enum gets to date-taken
         *  ordering today. The server doesn't support sorting by
         *  `originally_available_at` yet (sort enum is fixed at
         *  title/year/rating/created_at/updated_at), so this is a
         *  reasonable approximation until that lands. */
        fun defaultFor(libraryType: String): LibrarySort = when (libraryType) {
            "home_video", "photo", "dvr" -> LibrarySort("created_at", "desc")
            else -> DEFAULT
        }
    }
}

@HiltViewModel
class LibraryViewModel @Inject constructor(
    private val libraryRepo: LibraryRepository,
) : ViewModel() {

    private val _items = MutableStateFlow<List<MediaItem>>(emptyList())
    val items: StateFlow<List<MediaItem>> = _items

    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error

    // True once the first page of the current (library, sort, genre) query has
    // finished loading, so the fragment can show an empty state for a genuinely
    // empty library / zero-match filter instead of a blank grid.
    private val _loaded = MutableStateFlow(false)
    val loaded: StateFlow<Boolean> = _loaded

    private val _sort = MutableStateFlow(LibrarySort.DEFAULT)
    val sort: StateFlow<LibrarySort> = _sort

    private val _genre = MutableStateFlow<String?>(null)
    val genre: StateFlow<String?> = _genre

    private val _genres = MutableStateFlow<List<String>>(emptyList())
    val genres: StateFlow<List<String>> = _genres

    /** v2.5 Watch filter, sent as `?watch=` — one of [WatchFilter]'s values
     *  (unwatched / in_progress / watched), null = all items. */
    private val _watch = MutableStateFlow<String?>(null)
    val watch: StateFlow<String?> = _watch

    /** One-shot results of "Surprise me". */
    private val _events = MutableSharedFlow<LibraryEvent>(extraBufferCapacity = 2)
    val events: SharedFlow<LibraryEvent> = _events
    private var picking = false

    private var libraryId: String? = null
    private var offset = 0
    private var total = Int.MAX_VALUE
    private var loading = false
    private val pageSize = 50

    fun load(libraryId: String, libraryType: String = "") {
        // Re-entering this screen (detail → back) recreates the VIEW but not
        // this ViewModel, and the fragment calls load() from onViewCreated.
        // Blowing the accumulated pages away there defeated scroll restore
        // for anything past the first page: the grid came back with 50 items
        // while the saved position was ≥ 50, so restoreIfPending disarmed
        // without applying and the user was dumped at the top. The StateFlow
        // already holds the full list — a fresh collector gets it immediately,
        // so the correct move is to keep it.
        if (this.libraryId == libraryId && _items.value.isNotEmpty()) return
        this.libraryId = libraryId
        // Pick a per-type sort default the FIRST time this ViewModel
        // sees a library — re-loads keep whatever the user has
        // chosen via the sort menu. The fragment instance is per-
        // library (newInstance creates a fresh one), so this branch
        // only fires on the initial load.
        if (_items.value.isEmpty() && _sort.value == LibrarySort.DEFAULT) {
            _sort.value = LibrarySort.defaultFor(libraryType)
        }
        offset = 0
        total = Int.MAX_VALUE
        _items.value = emptyList()
        _loaded.value = false
        loadMore()
        if (_genres.value.isEmpty()) {
            viewModelScope.launch {
                _genres.value = libraryRepo.getGenres(libraryId)
            }
        }
    }

    fun setSort(sort: String, dir: String) {
        if (_sort.value.sort == sort && _sort.value.sortDir == dir) return
        _sort.value = LibrarySort(sort, dir)
        resetAndReload()
    }

    fun setGenre(genre: String?) {
        if (_genre.value == genre) return
        _genre.value = genre
        resetAndReload()
    }

    /**
     * Re-read the page holding [itemId] (same query) and swap in any items
     * whose content changed — watch_state / progress / unwatched counts after
     * the user marked or watched something on the detail screen. One request;
     * membership is left alone (an item that no longer matches a Watch filter
     * stays until the next full reload), so focus and scroll are undisturbed.
     */
    fun refreshAround(itemId: String) {
        val id = libraryId ?: return
        val index = _items.value.indexOfFirst { it.id == itemId }
        if (index < 0) return
        val pageOffset = (index / pageSize) * pageSize
        val s = _sort.value
        val g = _genre.value
        val w = _watch.value
        val gen = queryGeneration
        viewModelScope.launch {
            val fresh = try {
                libraryRepo.getItems(id, pageSize, pageOffset, s.sort, s.sortDir, g, w).first
            } catch (_: Exception) {
                return@launch // best-effort: keep what's on screen
            }
            if (gen != queryGeneration) return@launch
            val byId = fresh.associateBy { it.id }
            val cur = _items.value
            val next = cur.map { byId[it.id] ?: it }
            if (next != cur) _items.value = next
        }
    }

    /** Pick a random item under the current genre + watch filters (the
     *  server ignores sort) and ask the fragment to open it. */
    fun surpriseMe() {
        val id = libraryId ?: return
        if (picking) return
        picking = true
        val g = _genre.value
        val w = _watch.value
        viewModelScope.launch {
            try {
                val pick = libraryRepo.pickRandom(id, g, w)
                _events.tryEmit(if (pick != null) LibraryEvent.Open(pick.id, pick.type) else LibraryEvent.NothingToPick)
            } catch (_: Exception) {
                _events.tryEmit(LibraryEvent.PickFailed)
            } finally {
                picking = false
            }
        }
    }

    /** Anything that isn't a known [WatchFilter] value clears the filter. */
    fun setWatchFilter(watch: String?) {
        val w = WatchStateUi.parseWatchFilter(watch)
        if (_watch.value == w) return
        _watch.value = w
        resetAndReload()
    }

    /** Bumped whenever sort/genre/watch changes. In-flight page fetches capture
     *  the generation they were issued under and drop their results if it
     *  moved — without this, changing sort while page 1 was in flight (a)
     *  hit the `loading` guard and silently discarded the NEW query, and
     *  (b) let the stale response append old-sort items into the new list,
     *  interleaving two sort orders in one grid. */
    private var queryGeneration = 0

    private fun resetAndReload() {
        libraryId ?: return
        queryGeneration++
        offset = 0
        total = Int.MAX_VALUE
        _items.value = emptyList()
        _loaded.value = false
        // Unblock immediately: the in-flight request now belongs to a dead
        // generation and must not gate the fresh query.
        loading = false
        loadMore()
    }

    fun loadMore() {
        val id = libraryId ?: return
        if (loading || offset >= total) return
        loading = true

        val s = _sort.value
        val g = _genre.value
        val w = _watch.value
        val gen = queryGeneration
        viewModelScope.launch {
            try {
                val (page, count) = libraryRepo.getItems(id, pageSize, offset, s.sort, s.sortDir, g, w)
                if (gen != queryGeneration) return@launch // stale sort/genre
                total = count
                offset += page.size
                _items.value = _items.value + page
                _error.value = null
            } catch (e: Exception) {
                if (gen == queryGeneration && _items.value.isEmpty()) {
                    _error.value = e.message ?: "Failed to load"
                }
            } finally {
                if (gen == queryGeneration) {
                    loading = false
                    _loaded.value = true
                }
            }
        }
    }
}

sealed interface LibraryEvent {
    /** "Surprise me" picked this item. */
    data class Open(val id: String, val type: String) : LibraryEvent
    /** Nothing in the library matches the current filters. */
    data object NothingToPick : LibraryEvent
    /** The pick failed (network, or an older server without the endpoint). */
    data object PickFailed : LibraryEvent
}
