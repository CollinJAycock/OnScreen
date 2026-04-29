package tv.onscreen.android.ui.search

import android.util.Log
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import retrofit2.HttpException
import tv.onscreen.android.data.model.DiscoverItem
import tv.onscreen.android.data.model.Library
import tv.onscreen.android.data.model.SearchResult
import tv.onscreen.android.data.prefs.SearchFilters
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.DiscoverRepository
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.LibraryRepository
import javax.inject.Inject

@HiltViewModel
class SearchViewModel @Inject constructor(
    private val itemRepo: ItemRepository,
    private val libraryRepo: LibraryRepository,
    private val discoverRepo: DiscoverRepository,
    private val prefs: ServerPrefs,
) : ViewModel() {

    /** Raw library matches — items already in the user's collection.
     *  Consumers should bind to [visibleResults] instead so the type-
     *  filter checkboxes apply. */
    private val _results = MutableStateFlow<List<SearchResult>>(emptyList())
    val results: StateFlow<List<SearchResult>> = _results

    /** Reactive filter state, persisted via DataStore. Defaults match
     *  the web client (movie + show on, episode + track off). */
    val filters: StateFlow<SearchFilters> = prefs.searchFilters.stateIn(
        scope = viewModelScope,
        started = SharingStarted.Eagerly,
        initialValue = SearchFilters(movie = true, show = true, episode = false, track = false),
    )

    /** Library results filtered by the type checkboxes. Matches the
     *  web /search rules: album + artist piggyback on the Track box,
     *  season piggybacks on Show. Unknown types pass through so a
     *  future media type renders the moment the API returns it. */
    val visibleResults: StateFlow<List<SearchResult>> = combine(_results, filters) { rows, f ->
        rows.filter { r ->
            when (r.type) {
                "movie" -> f.movie
                "show" -> f.show
                "season" -> f.show
                "episode" -> f.episode
                "artist" -> f.track
                "album" -> f.track
                "track" -> f.track
                else -> true
            }
        }
    }.stateIn(
        scope = viewModelScope,
        started = SharingStarted.Eagerly,
        initialValue = emptyList(),
    )

    /** TMDB-discover matches — titles the user could request. The
     *  list is filtered to drop in_library entries client-side
     *  (they're already in [results]; rendering both would duplicate). */
    private val _discover = MutableStateFlow<List<DiscoverItem>>(emptyList())
    val discover: StateFlow<List<DiscoverItem>> = _discover

    /** Reason TMDB discover came back empty — surfaced in the UI so
     *  the user knows whether requests are even possible on this
     *  server. Empty = no error (either succeeded or simply has no
     *  TMDB matches). Non-empty = a configurable problem worth
     *  showing. */
    private val _discoverError = MutableStateFlow<String?>(null)
    val discoverError: StateFlow<String?> = _discoverError

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

    /** Toggle a single filter checkbox. Persisted via DataStore so
     *  the user's choices survive app restart, matching the web
     *  client's localStorage persistence. */
    fun toggleFilter(type: FilterType) {
        viewModelScope.launch {
            val current = filters.value
            val next = when (type) {
                FilterType.MOVIE -> current.copy(movie = !current.movie)
                FilterType.SHOW -> current.copy(show = !current.show)
                FilterType.EPISODE -> current.copy(episode = !current.episode)
                FilterType.TRACK -> current.copy(track = !current.track)
            }
            prefs.setSearchFilters(next)
        }
    }

    enum class FilterType { MOVIE, SHOW, EPISODE, TRACK }

    fun search(query: String) {
        lastQuery = query
        searchJob?.cancel()

        if (query.length < 2) {
            _results.value = emptyList()
            _discover.value = emptyList()
            _discoverError.value = null
            return
        }

        val libraryId = _scope.value?.id
        searchJob = viewModelScope.launch {
            delay(300) // Debounce — wait for user to stop typing.

            val libraryDeferred = async {
                try { itemRepo.search(query, libraryId = libraryId) }
                catch (e: Exception) {
                    Log.w(TAG, "library search failed", e)
                    emptyList()
                }
            }

            // Discover (TMDB) is skipped when scoped to a single
            // library — cross-library suggestions are noise when the
            // user has narrowed to a specific shelf. Failures are
            // surfaced via [discoverError] so the user knows the
            // Request row is unavailable and why; previously they
            // were swallowed silently and the row just never
            // appeared, leaving the user assuming nothing matched.
            val discoverDeferred = async {
                if (libraryId != null) {
                    DiscoverResult(emptyList(), null)
                } else {
                    try {
                        DiscoverResult(discoverRepo.search(query), null)
                    } catch (e: Exception) {
                        val reason = explainDiscoverFailure(e)
                        Log.w(TAG, "discover search failed: $reason", e)
                        DiscoverResult(emptyList(), reason)
                    }
                }
            }
            val (lib, disc) = awaitAll(libraryDeferred, discoverDeferred)
            @Suppress("UNCHECKED_CAST")
            _results.value = lib as List<SearchResult>
            val discoverResult = disc as DiscoverResult
            _discover.value = discoverResult.items.filter { !it.in_library }
            _discoverError.value = discoverResult.errorReason
        }
    }

    private data class DiscoverResult(
        val items: List<DiscoverItem>,
        val errorReason: String?,
    )

    /** Map a discover-call exception to a short human-readable
     *  reason. The most common cases are TMDB-not-configured (404
     *  / 503 from the server) — for those we use a friendlier
     *  message; otherwise return the raw exception text. */
    private fun explainDiscoverFailure(e: Exception): String {
        if (e is HttpException) {
            return when (e.code()) {
                404, 503 -> "Discover unavailable — TMDB not configured on this server"
                401, 403 -> "Discover requires sign-in"
                else -> "Discover failed (HTTP ${e.code()})"
            }
        }
        return e.message ?: "Discover failed"
    }

    companion object {
        private const val TAG = "SearchViewModel"
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
