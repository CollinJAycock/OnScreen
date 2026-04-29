package tv.onscreen.mobile.ui.search

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
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
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
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
import tv.onscreen.mobile.data.model.SearchResult
import tv.onscreen.mobile.data.prefs.SearchFilters
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
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

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SearchScreen(
    onOpenItem: (String) -> Unit,
    onBack: () -> Unit,
    vm: SearchViewModel = hiltViewModel(),
) {
    val ui by vm.state.collectAsState()
    val filters by vm.filters.collectAsState()

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
                value = ui.query,
                onValueChange = vm::onQueryChange,
                singleLine = true,
                placeholder = { Text("Search movies, shows, music…") },
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp, vertical = 8.dp),
            )

            Row(
                modifier = Modifier
                    .horizontalScroll(rememberScrollState())
                    .padding(horizontal = 16.dp, vertical = 4.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                FilterChipRow(
                    label = "Movies",
                    selected = filters.movie,
                    onToggle = { vm.toggleFilter(SearchViewModel.FilterType.MOVIE) },
                )
                FilterChipRow(
                    label = "TV Shows",
                    selected = filters.show,
                    onToggle = { vm.toggleFilter(SearchViewModel.FilterType.SHOW) },
                )
                FilterChipRow(
                    label = "Episodes",
                    selected = filters.episode,
                    onToggle = { vm.toggleFilter(SearchViewModel.FilterType.EPISODE) },
                )
                FilterChipRow(
                    label = "Music",
                    selected = filters.track,
                    onToggle = { vm.toggleFilter(SearchViewModel.FilterType.TRACK) },
                )
            }

            Spacer(Modifier.width(4.dp))

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
