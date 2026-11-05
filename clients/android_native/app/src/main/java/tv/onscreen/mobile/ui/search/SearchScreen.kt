package tv.onscreen.mobile.ui.search

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import coil.compose.AsyncImage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.artworkUrl
import tv.onscreen.mobile.data.model.SearchResult
import tv.onscreen.mobile.data.prefs.SearchFilters
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.ui.components.EmptyState
import tv.onscreen.mobile.ui.components.ErrorState
import tv.onscreen.mobile.ui.components.LoadingState
import javax.inject.Inject

@HiltViewModel
class SearchViewModel @Inject constructor(
    private val repo: ItemRepository,
    private val prefs: ServerPrefs,
    savedState: SavedStateHandle,
) : ViewModel() {

    /** Set when opened from a library's search action
     *  ([tv.onscreen.mobile.ui.nav.Routes.LIBRARY_SEARCH]): every query
     *  carries `library_id` so results come from that library only. Null =
     *  the global search across every library. */
    private val libraryId: String? = savedState.get<String>("libraryId")?.takeIf { it.isNotBlank() }

    private val _state = MutableStateFlow(
        SearchUi(
            libraryScoped = libraryId != null,
            libraryName = savedState.get<String>("libraryName")?.takeIf { it.isNotBlank() },
        ),
    )
    private val raw: StateFlow<SearchUi> = _state.asStateFlow()

    init {
        // Resolve the server URL once so result rows can build artwork
        // URLs for their leading thumbnails.
        viewModelScope.launch {
            _state.value = _state.value.copy(serverUrl = prefs.getServerUrl().orEmpty())
        }
    }

    /** Persisted type-filter state. Defaults match the web client and
     *  the TV client: movie + show on, episode + track off. Album +
     *  artist piggyback on the track chip in [visibleResults], same as
     *  web/TV — keeps the visible chip count to four. */
    val filters: StateFlow<SearchFilters> = prefs.searchFilters.stateIn(
        scope = viewModelScope,
        started = SharingStarted.Eagerly,
        initialValue = SearchFilters(movie = true, show = true, episode = false, track = false),
    )

    /** Filter-applied view of the result list. Unknown types fall
     *  through so a future server media type renders without a code
     *  bump on the client. A library-scoped search shows everything the
     *  library matched: the chips aren't shown there (in a music library
     *  the default chips would hide every result). */
    val state: StateFlow<SearchUi> = combine(raw, filters) { ui, f ->
        if (ui.libraryScoped) return@combine ui
        ui.copy(
            results = ui.results.filter { r ->
                when (r.type) {
                    "movie" -> f.movie
                    "show", "season" -> f.show
                    "episode" -> f.episode
                    "artist", "album", "track" -> f.track
                    else -> true
                }
            },
        )
    }.stateIn(viewModelScope, SharingStarted.Eagerly, SearchUi())

    private var job: Job? = null

    /** Bumped per search started (and per keystroke). A response for an
     *  older query is dropped even if it lands after a newer one started:
     *  cancelling the job alone doesn't stop a call that has already
     *  returned and is resuming. */
    private var seq = 0

    fun onQueryChange(q: String) {
        _state.value = _state.value.copy(query = q)
        job?.cancel()
        seq++
        if (q.length < 2) {
            _state.value = _state.value.copy(results = emptyList(), loading = false, error = null)
            return
        }
        runSearch(q, debounce = true)
    }

    /** Re-run the current query — used by the error-state Retry button. */
    fun retry() {
        val q = _state.value.query
        if (q.length < 2) return
        job?.cancel()
        runSearch(q, debounce = false)
    }

    private fun runSearch(q: String, debounce: Boolean) {
        val mine = ++seq
        job = viewModelScope.launch {
            if (debounce) delay(300)
            _state.value = _state.value.copy(loading = true, error = null)
            try {
                val r = repo.search(q, libraryId = libraryId)
                if (mine != seq) return@launch
                _state.value = _state.value.copy(loading = false, results = r)
            } catch (e: CancellationException) {
                // Superseded by a newer keystroke: not an error to show.
                throw e
            } catch (e: Exception) {
                if (mine != seq) return@launch
                _state.value = _state.value.copy(loading = false, error = e.message)
            }
        }
    }

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
}

data class SearchUi(
    val query: String = "",
    val loading: Boolean = false,
    val results: List<SearchResult> = emptyList(),
    val serverUrl: String = "",
    val error: String? = null,
    /** Searching one library (from its screen) rather than all of them. */
    val libraryScoped: Boolean = false,
    /** That library's name, when the library screen knew it. */
    val libraryName: String? = null,
) {
    /** "Search Films" when scoped to a named library. */
    val title: String
        get() = when {
            !libraryScoped -> "Search"
            libraryName != null -> "Search $libraryName"
            else -> "Search this library"
        }
}

/**
 * Library search. A single query field over the local library via
 * [SearchViewModel] (auto-debounced), with the persisted type-filter
 * chips beneath it. Opened from a library's search action it searches
 * that library only, without the chips; results open the same way
 * either way.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SearchScreen(
    onOpenItem: (String) -> Unit,
    onBack: () -> Unit,
    vm: SearchViewModel = hiltViewModel(),
) {
    val ui by vm.state.collectAsStateWithLifecycle()
    val filters by vm.filters.collectAsStateWithLifecycle()

    // Read the query from the ViewModel's state instead of a plain
    // remember. The SearchViewModel outlives this composable, so opening a
    // result and pressing Back re-enters SearchScreen with an empty local
    // field even though the VM still holds the query (and its results).
    // Sourcing the field from ui.query restores the text on return; the VM
    // already stores every keystroke via onQueryChange.
    val query = ui.query

    // From a library's search action the point of the tap is to type: put
    // the cursor in the field. Not on a return from a result (the query is
    // already there).
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) {
        if (ui.libraryScoped && ui.query.isEmpty()) focus.requestFocus()
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(ui.title, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            OutlinedTextField(
                value = query,
                onValueChange = vm::onQueryChange,
                singleLine = true,
                placeholder = {
                    Text(
                        if (ui.libraryScoped) "Titles in ${ui.libraryName ?: "this library"}…"
                        else "Search movies, shows, music…",
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                },
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp, vertical = 8.dp)
                    .focusRequester(focus),
            )

            LibraryResults(
                ui = ui,
                filters = filters,
                onToggleFilter = vm::toggleFilter,
                onOpenItem = onOpenItem,
                onRetry = vm::retry,
            )
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
private fun LibraryResults(
    ui: SearchUi,
    filters: SearchFilters,
    onToggleFilter: (SearchViewModel.FilterType) -> Unit,
    onOpenItem: (String) -> Unit,
    onRetry: () -> Unit,
) {
    if (!ui.libraryScoped) FlowRow(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        FilterChipRow(
            label = "Movies",
            selected = filters.movie,
            onToggle = { onToggleFilter(SearchViewModel.FilterType.MOVIE) },
        )
        FilterChipRow(
            label = "TV Shows",
            selected = filters.show,
            onToggle = { onToggleFilter(SearchViewModel.FilterType.SHOW) },
        )
        FilterChipRow(
            label = "Episodes",
            selected = filters.episode,
            onToggle = { onToggleFilter(SearchViewModel.FilterType.EPISODE) },
        )
        FilterChipRow(
            label = "Music",
            selected = filters.track,
            onToggle = { onToggleFilter(SearchViewModel.FilterType.TRACK) },
        )
    }

    Box(modifier = Modifier.fillMaxSize()) {
        when {
            ui.loading && ui.results.isEmpty() -> LoadingState()
            ui.error != null && ui.results.isEmpty() ->
                ErrorState(ui.error, onRetry = onRetry)
            ui.results.isEmpty() && ui.query.length >= 2 && !ui.loading ->
                EmptyState(
                    if (ui.libraryScoped && ui.libraryName != null) {
                        "No results for \"${ui.query}\" in ${ui.libraryName}"
                    } else {
                        "No results for \"${ui.query}\""
                    },
                )
            ui.results.isEmpty() ->
                EmptyState(
                    if (ui.libraryScoped) "Type to search ${ui.libraryName ?: "this library"}."
                    else "Type to search your library.",
                )
            else -> LazyColumn(contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp)) {
                items(ui.results, key = { it.id }) { r ->
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .clickable { onOpenItem(r.id) }
                            .padding(vertical = 8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        SearchPosterThumb(
                            serverUrl = ui.serverUrl,
                            path = r.poster_path ?: r.thumb_path,
                            contentDescription = r.title,
                        )
                        Spacer(Modifier.width(12.dp))
                        Column {
                            Text(r.title, style = MaterialTheme.typography.bodyLarge)
                            Text(
                                r.type,
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }
        }
    }
}

/** Small leading poster thumbnail for a search result row. Falls back
 *  to a surface-variant placeholder when artwork or the server URL is
 *  missing. */
@Composable
private fun SearchPosterThumb(serverUrl: String, path: String?, contentDescription: String?) {
    Box(
        modifier = Modifier
            .width(40.dp)
            .height(60.dp)
            .clip(RoundedCornerShape(4.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant),
    ) {
        if (!path.isNullOrBlank() && serverUrl.isNotEmpty()) {
            AsyncImage(
                model = artworkUrl(serverUrl, path, width = 120),
                contentDescription = contentDescription,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun FilterChipRow(label: String, selected: Boolean, onToggle: () -> Unit) {
    FilterChip(
        selected = selected,
        onClick = onToggle,
        label = { Text(label) },
        colors = FilterChipDefaults.filterChipColors(),
    )
}
