package tv.onscreen.android.ui.search

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.DiscoverItem
import tv.onscreen.android.data.model.Library
import tv.onscreen.android.data.model.SearchResult
import tv.onscreen.android.data.repository.DiscoverRepository
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.LibraryRepository
import javax.inject.Inject

@HiltViewModel
class SearchViewModel @Inject constructor(
    private val itemRepo: ItemRepository,
    private val libraryRepo: LibraryRepository,
    private val discoverRepo: DiscoverRepository,
) : ViewModel() {

    /** Library matches — items already in the user's collection. */
    private val _results = MutableStateFlow<List<SearchResult>>(emptyList())
    val results: StateFlow<List<SearchResult>> = _results

    /** TMDB-discover matches — titles the user could request. The
     *  list is filtered to drop in_library entries client-side
     *  (they're already in [results]; rendering both would duplicate). */
    private val _discover = MutableStateFlow<List<DiscoverItem>>(emptyList())
    val discover: StateFlow<List<DiscoverItem>> = _discover

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
            _discover.value = emptyList()
            return
        }

        val libraryId = _scope.value?.id
        searchJob = viewModelScope.launch {
            delay(300) // Debounce — wait for user to stop typing.

            // Library + discover fan-out in parallel. Library failures
            // surface as an empty list; discover failures (TMDB not
            // configured, rate-limited) also surface as empty so the
            // user still sees library hits even if the request layer
            // is unavailable.
            val libraryDeferred = async {
                try { itemRepo.search(query, libraryId = libraryId) } catch (_: Exception) { emptyList() }
            }
            // Don't bother hitting TMDB when scoped to a single
            // library — the user is browsing locally and the discover
            // hits would surface unrelated titles.
            val discoverDeferred = async {
                if (libraryId != null) emptyList()
                else try {
                    discoverRepo.search(query)
                } catch (_: Exception) {
                    emptyList()
                }
            }
            val (lib, disc) = awaitAll(libraryDeferred, discoverDeferred)
            @Suppress("UNCHECKED_CAST")
            _results.value = lib as List<SearchResult>
            @Suppress("UNCHECKED_CAST")
            _discover.value = (disc as List<DiscoverItem>).filter { !it.in_library }
        }
    }

    /** Create a media request for a discover item. Updates the
     *  [discover] state so the row's chip flips from "Request" to
     *  "Pending" without a re-search. Failures bubble up to the
     *  caller for toast surfacing. */
    fun request(item: DiscoverItem, onResult: (Result<Unit>) -> Unit) {
        viewModelScope.launch {
            try {
                val created = discoverRepo.createRequest(item.type, item.tmdb_id)
                _discover.value = _discover.value.map { d ->
                    if (d.tmdb_id == item.tmdb_id && d.type == item.type) {
                        d.copy(
                            has_active_request = true,
                            active_request_id = created.id,
                            active_request_status = created.status,
                        )
                    } else d
                }
                onResult(Result.success(Unit))
            } catch (e: Exception) {
                onResult(Result.failure(e))
            }
        }
    }
}
