package tv.onscreen.android.ui.browse

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.MediaItem
import tv.onscreen.android.data.repository.LibraryRepository
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

    private val _sort = MutableStateFlow(LibrarySort.DEFAULT)
    val sort: StateFlow<LibrarySort> = _sort

    private val _genre = MutableStateFlow<String?>(null)
    val genre: StateFlow<String?> = _genre

    private val _genres = MutableStateFlow<List<String>>(emptyList())
    val genres: StateFlow<List<String>> = _genres

    private var libraryId: String? = null
    private var offset = 0
    private var total = Int.MAX_VALUE
    private var loading = false
    private val pageSize = 50

    fun load(libraryId: String, libraryType: String = "") {
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

    private fun resetAndReload() {
        libraryId ?: return
        offset = 0
        total = Int.MAX_VALUE
        _items.value = emptyList()
        loadMore()
    }

    fun loadMore() {
        val id = libraryId ?: return
        if (loading || offset >= total) return
        loading = true

        val s = _sort.value
        val g = _genre.value
        viewModelScope.launch {
            try {
                val (page, count) = libraryRepo.getItems(id, pageSize, offset, s.sort, s.sortDir, g)
                total = count
                offset += page.size
                _items.value = _items.value + page
                _error.value = null
            } catch (e: Exception) {
                if (_items.value.isEmpty()) _error.value = e.message ?: "Failed to load"
            } finally {
                loading = false
            }
        }
    }
}
