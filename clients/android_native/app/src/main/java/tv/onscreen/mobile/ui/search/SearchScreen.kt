package tv.onscreen.mobile.ui.search

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
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
import tv.onscreen.mobile.data.model.DiscoverItem
import tv.onscreen.mobile.data.model.SearchResult
import tv.onscreen.mobile.data.prefs.SearchFilters
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.ui.discover.DiscoverViewModel
import javax.inject.Inject

@HiltViewModel
class SearchViewModel @Inject constructor(
    private val repo: ItemRepository,
    private val prefs: ServerPrefs,
) : ViewModel() {

    private val _state = MutableStateFlow(SearchUi())
    private val raw: StateFlow<SearchUi> = _state.asStateFlow()

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
            _state.value = _state.value.copy(results = emptyList(), loading = false)
            return
        }
        job = viewModelScope.launch {
            delay(300)
            _state.value = _state.value.copy(loading = true)
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
    val error: String? = null,
)

/**
 * Combined Search + Discover screen. Two tabs share a single query
 * text field at the top:
 *   - **Library** (default) — auto-debounced search of the local
 *     library via [SearchViewModel].
 *   - **Discover** — TMDB lookup for titles to add via [DiscoverViewModel].
 *     Doesn't auto-fire (TMDB bills against a daily cap), so the user
 *     taps "Search TMDB" to run it.
 *
 * Replaces the standalone Discover screen — the two surfaces were
 * doing the same job ("find me a thing") with different sources, and
 * they belonged together once Hub's overflow menu got crowded.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SearchScreen(
    onOpenItem: (String) -> Unit,
    onBack: () -> Unit,
    vm: SearchViewModel = hiltViewModel(),
    discoverVm: DiscoverViewModel = hiltViewModel(),
) {
    val ui by vm.state.collectAsState()
    val filters by vm.filters.collectAsState()
    val discoverUi by discoverVm.state.collectAsState()

    var query by remember { mutableStateOf("") }
    var tabIndex by remember { mutableIntStateOf(0) }

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
            // Single shared text field. Push every keystroke to both
            // VMs so switching tabs preserves the typed query and
            // either tab's body reflects it. SearchViewModel debounces
            // internally; DiscoverViewModel only fires when the user
            // taps "Search TMDB" (rate-limit guard).
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp, vertical = 8.dp),
            ) {
                OutlinedTextField(
                    value = query,
                    onValueChange = { q ->
                        query = q
                        vm.onQueryChange(q)
                        discoverVm.onQueryChanged(q)
                    },
                    singleLine = true,
                    placeholder = { Text("Search movies, shows, music…") },
                    modifier = Modifier.weight(1f),
                )
                if (tabIndex == 1) {
                    // Discover tab: explicit fire button. Only surfaced
                    // here so users on the library tab don't think they
                    // need to tap something to make autocomplete work.
                    Spacer(Modifier.width(8.dp))
                    IconButton(onClick = discoverVm::search) {
                        Icon(Icons.Default.Search, contentDescription = "Search TMDB")
                    }
                }
            }

            TabRow(selectedTabIndex = tabIndex) {
                Tab(
                    selected = tabIndex == 0,
                    onClick = { tabIndex = 0 },
                    text = { Text("Library") },
                )
                Tab(
                    selected = tabIndex == 1,
                    onClick = { tabIndex = 1 },
                    text = { Text("Discover") },
                )
            }

            when (tabIndex) {
                0 -> LibraryTab(
                    ui = ui,
                    filters = filters,
                    onToggleFilter = vm::toggleFilter,
                    onOpenItem = onOpenItem,
                )
                else -> DiscoverTab(
                    query = query,
                    ui = discoverUi,
                    onRequest = discoverVm::request,
                )
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun LibraryTab(
    ui: SearchUi,
    filters: SearchFilters,
    onToggleFilter: (SearchViewModel.FilterType) -> Unit,
    onOpenItem: (String) -> Unit,
) {
    Row(
        modifier = Modifier
            .horizontalScroll(rememberScrollState())
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

    LazyColumn(contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp)) {
        items(ui.results, key = { it.id }) { r ->
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable { onOpenItem(r.id) }
                    .padding(vertical = 12.dp),
            ) {
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

@Composable
private fun DiscoverTab(
    query: String,
    ui: tv.onscreen.mobile.ui.discover.DiscoverUi,
    onRequest: (DiscoverItem) -> Unit,
) {
    Column(modifier = Modifier.padding(16.dp)) {
        ui.error?.let { err ->
            Text(
                err,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(vertical = 8.dp),
            )
        }
        Box(modifier = Modifier.fillMaxSize()) {
            when {
                ui.loading -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                ui.results.isEmpty() && query.isNotBlank() && ui.error == null ->
                    Text(
                        "Tap the search icon to look up \"$query\" on TMDB.",
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.align(Alignment.Center),
                    )
                ui.results.isEmpty() ->
                    Text(
                        "Search TMDB to find titles you can request to add to the library.",
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.align(Alignment.Center),
                    )
                else -> LazyVerticalGrid(
                    columns = GridCells.Adaptive(minSize = 160.dp),
                    contentPadding = PaddingValues(vertical = 8.dp),
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                    verticalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    items(ui.results, key = { it.tmdb_id }) { item ->
                        DiscoverCard(
                            item = item,
                            submitting = item.tmdb_id in ui.submitting,
                            submitted = item.tmdb_id in ui.submitted,
                            onRequest = { onRequest(item) },
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun DiscoverCard(
    item: DiscoverItem,
    submitting: Boolean,
    submitted: Boolean,
    onRequest: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        if (item.poster_url != null) {
            AsyncImage(
                model = item.poster_url,
                contentDescription = item.title,
                modifier = Modifier
                    .fillMaxWidth()
                    .height(240.dp)
                    .clip(RoundedCornerShape(6.dp)),
            )
        } else {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(240.dp)
                    .clip(RoundedCornerShape(6.dp))
                    .padding(8.dp),
            ) {
                Text(
                    text = item.title.take(2).uppercase(),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.headlineLarge,
                    modifier = Modifier.align(Alignment.Center),
                )
            }
        }
        Spacer(Modifier.height(6.dp))
        Text(
            text = item.title,
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.Medium,
            maxLines = 2,
        )
        if (item.year != null) {
            Text(
                text = item.year.toString(),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(4.dp))
        val (label, enabled) = when {
            item.in_library -> "In library" to false
            submitted || item.has_active_request -> "Requested" to false
            submitting -> "Sending…" to false
            else -> "Request" to true
        }
        Button(
            onClick = onRequest,
            enabled = enabled,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(label)
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
