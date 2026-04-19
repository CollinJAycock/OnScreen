package tv.onscreen.android.ui.search

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.Library
import tv.onscreen.android.data.model.SearchResult
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.LibraryRepository
import javax.inject.Inject

@HiltViewModel
class SearchViewModel @Inject constructor(
    private val itemRepo: ItemRepository,
    private val libraryRepo: LibraryRepository,
) : ViewModel() {

    private val _results = MutableStateFlow<List<SearchResult>>(emptyList())
    val results: StateFlow<List<SearchResult>> = _results

    private val _libraries = MutableStateFlow<List<Library>>(emptyList())
    val libraries: StateFlow<List<Library>> = _libraries

    private val _scope = MutableStateFlow<Library?>(null)
    val scope: StateFlow<Library?> = _scope

    private var searchJob: Job? = null
    private var lastQuery: String = ""

    init {
        viewModelScope.launch {
            try { _libraries.value = libraryRepo.getLibraries() } catch (_: Exception) {}
        }
    }

    fun setScope(library: Library?) {
        _scope.value = library
        if (lastQuery.isNotEmpty()) search(lastQuery)
    }

    fun search(query: String) {
        lastQuery = query
        searchJob?.cancel()

        if (query.length < 2) {
            _results.value = emptyList()
            return
        }

        val libraryId = _scope.value?.id
        searchJob = viewModelScope.launch {
            delay(300) // Debounce — wait for user to stop typing.
            try {
                _results.value = itemRepo.search(query, libraryId = libraryId)
            } catch (_: Exception) {
                _results.value = emptyList()
            }
        }
    }
}
