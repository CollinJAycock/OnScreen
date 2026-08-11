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
import tv.onscreen.android.data.model.Library
import tv.onscreen.android.data.model.SearchResult
import tv.onscreen.android.data.prefs.SearchFilters
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.LibraryRepository
import javax.inject.Inject

@HiltViewModel
class SearchViewModel @Inject constructor(
    private val itemRepo: ItemRepository,
    private val libraryRepo: LibraryRepository,
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


    /** Why the LIBRARY search failed, or null. Without this a failed search was
     *  indistinguishable from an empty one: the exception was swallowed, an
     *  empty list was published, and the screen said "No results found" — the
     *  app asserting the user does not own a title that is sitting in their
     *  library, with no error and no retry. Worst on a library-scoped search,
     *  where there would otherwise be no signal at all. */
    private val _searchError = MutableStateFlow<String?>(null)
    val searchError: StateFlow<String?> = _searchError

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
            _searchError.value = null
            return
        }

        val libraryId = _scope.value?.id
        searchJob = viewModelScope.launch {
            delay(300) // Debounce — wait for user to stop typing.

            val libraryDeferred = async {
                try {
                    LibraryResult(itemRepo.search(query, libraryId = libraryId), null)
                } catch (e: Exception) {
                    Log.w(TAG, "library search failed", e)
                    LibraryResult(emptyList(), explainSearchFailure(e))
                }
            }

            // Content requests were REMOVED from the TV client (2026-08-06).
            // The Amazon Appstore rejected 1.1.0-1.1.2 under its Deceptive
            // and Malicious Behavior policy — "facilitates the ability to
            // save, convert, stream or download media from third-party
            // sources" — because Search surfaced TMDB titles the user does
            // not own alongside an action to have them acquired. Rewording
            // it changed nothing: the objection is to the capability, not
            // the label. Search is now strictly a search of the user's own
            // library. The request flow still exists on the web app, where
            // the operator (not a store reviewer) is the audience.
            val libResult = libraryDeferred.await()
            // On failure CLEAR the results. Keeping them was meant to survive a
            // transient blip mid-typing, but it left the previous query's hits
            // sitting under the "In your library" header while the error row
            // said the search had failed — so the screen asserted that stale
            // list matched the new query. The error row is the honest state; an
            // empty list beneath it is not a regression because the row itself
            // explains why, and "No results found" is separately suppressed.
            _results.value = libResult.items
            _searchError.value = libResult.error
        }
    }

    private data class LibraryResult(
        val items: List<SearchResult>,
        val error: String?,
    )




    /** Map a library-search exception to a short human-readable reason. The
     *  raw exception text is unhelpful on a 10-foot screen, and an error that
     *  is about a different subsystem, shown here, misleads the user into
     *  concluding their search legitimately found nothing. */
    private fun explainSearchFailure(e: Exception): String {
        if (e is HttpException) {
            return when (e.code()) {
                401, 403 -> "Sign in again to search your library"
                else -> "Couldn't search your library (HTTP ${e.code()})"
            }
        }
        return "Couldn't reach the server to search your library"
    }

    companion object {
        private const val TAG = "SearchViewModel"
    }
}
