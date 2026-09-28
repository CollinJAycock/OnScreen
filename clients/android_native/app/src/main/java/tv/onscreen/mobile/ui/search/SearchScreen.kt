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
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import coil.compose.AsyncImage
import dagger.hilt.android.lifecycle.HiltViewModel
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
) : ViewModel() {

    private val _state = MutableStateFlow(SearchUi())
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
     *  bump on the client. */
    val state: StateFlow<SearchUi> = combine(raw, filters) { ui, f ->
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

    fun onQueryChange(q: String) {
        _state.value = _state.value.copy(query = q)
        job?.cancel()
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
        job = viewModelScope.launch {
            if (debounce) delay(300)
            _state.value = _state.value.copy(loading = true, error = null)
            try {
                val r = repo.search(q)
                _state.value = _state.value.copy(loading = false, results = r)
            } catch (e: Exception) {
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
)

/**
 * Library search. A single query field over the local library via
 * [SearchViewModel] (auto-debounced), with the persisted type-filter
 * chips beneath it.
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

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Search") },
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
                placeholder = { Text("Search movies, shows, music…") },
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp, vertical = 8.dp),
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
    FlowRow(
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
                EmptyState("No results for \"${ui.query}\"")
            ui.results.isEmpty() ->
                EmptyState("Type to search your library.")
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
