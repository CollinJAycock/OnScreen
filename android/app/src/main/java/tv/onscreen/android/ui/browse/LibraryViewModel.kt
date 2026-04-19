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

    fun load(libraryId: String) {
        this.libraryId = libraryId
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
