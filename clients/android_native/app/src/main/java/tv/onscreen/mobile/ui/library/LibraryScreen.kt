package tv.onscreen.mobile.ui.library

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextAlign
import coil.compose.AsyncImage
import tv.onscreen.mobile.data.artworkUrl
import tv.onscreen.mobile.ui.components.EmptyState
import tv.onscreen.mobile.ui.components.ErrorState
import tv.onscreen.mobile.ui.components.LoadingState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Place
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.model.MediaItem
import tv.onscreen.mobile.data.repository.LibraryRepository
import javax.inject.Inject

@HiltViewModel
class LibraryViewModel @Inject constructor(
    private val repo: LibraryRepository,
    private val prefs: tv.onscreen.mobile.data.prefs.ServerPrefs,
) : ViewModel() {

    private val _state = MutableStateFlow(LibraryUi())
    val state: StateFlow<LibraryUi> = _state.asStateFlow()

    private var currentLibrary: String? = null

    /** Initial load. Idempotent: re-entering a library that's already loaded
     *  keeps the existing items (and the grid's scroll position) rather than
     *  blanking back to a full-screen spinner on every drill-in/return. */
    fun load(libraryId: String) {
        if (currentLibrary == libraryId && _state.value.items.isNotEmpty()) return
        currentLibrary = libraryId
        fetchFirstPage(keepItems = false)
    }

    /** Pull-to-refresh: reload page 0 but keep the current items visible under
     *  the refresh indicator (no full-screen spinner flash). */
    fun refresh() = fetchFirstPage(keepItems = true)

    private fun fetchFirstPage(keepItems: Boolean) {
        val libraryId = currentLibrary ?: return
        viewModelScope.launch {
            _state.value = _state.value.copy(
                loading = true,
                error = null,
                items = if (keepItems) _state.value.items else emptyList(),
            )
            try {
                val (items, total) = repo.getItems(libraryId, limit = PAGE_SIZE, offset = 0)
                _state.value = LibraryUi(
                    loading = false,
                    items = items,
                    total = total,
                    serverUrl = prefs.getServerUrl().orEmpty(),
                )
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false, error = e.message)
            }
        }
    }

    /** Append the next page as the grid nears its end. No-ops while a page is
     *  already loading or everything is loaded. */
    fun loadMore() {
        val s = _state.value
        val libraryId = currentLibrary ?: return
        if (s.loading || s.loadingMore || s.items.size >= s.total) return
        viewModelScope.launch {
            _state.value = _state.value.copy(loadingMore = true)
            try {
                val (more, total) = repo.getItems(libraryId, limit = PAGE_SIZE, offset = s.items.size)
                // distinctBy: server pages can overlap when the sort column
                // ties (LIMIT/OFFSET over a non-unique ORDER BY) — a repeated
                // id would crash the grid's stable keys. If a page adds
                // nothing new, treat the list as complete so the scroll
                // trigger can't refetch the same offset forever.
                val merged = (_state.value.items + more).distinctBy { it.id }
                _state.value = _state.value.copy(
                    loadingMore = false,
                    items = merged,
                    total = if (merged.size == s.items.size) merged.size else total,
                )
            } catch (_: Exception) {
                // Leave loaded items in place; the next scroll re-attempts.
                _state.value = _state.value.copy(loadingMore = false)
            }
        }
    }

    private companion object {
        const val PAGE_SIZE = 100
    }
}

data class LibraryUi(
    val loading: Boolean = false,
    val loadingMore: Boolean = false,
    val items: List<MediaItem> = emptyList(),
    val total: Int = 0,
    /** Server origin needed to build the per-item artwork URL — same pattern
     *  as HubScreen. */
    val serverUrl: String = "",
    val error: String? = null,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LibraryScreen(
    libraryId: String,
    onOpenItem: (String) -> Unit,
    onOpenPhoto: (String) -> Unit,
    onOpenPhotoExtras: ((String) -> Unit)? = null,
    onBack: () -> Unit,
    vm: LibraryViewModel = hiltViewModel(),
) {
    LaunchedEffect(libraryId) { vm.load(libraryId) }
    val ui by vm.state.collectAsState()
    val gridState = rememberLazyGridState()

    // Infinite scroll: when the last visible cell nears the end of the loaded
    // set and the server has more, fetch the next page.
    val shouldLoadMore by remember {
        derivedStateOf {
            val last = gridState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: -1
            ui.items.isNotEmpty() && ui.items.size < ui.total && last >= ui.items.size - 8
        }
    }
    LaunchedEffect(shouldLoadMore) { if (shouldLoadMore) vm.loadMore() }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Library") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
                actions = {
                    // Photo-extras (timeline / geotagged) entry point. The
                    // extras screen gracefully empties on non-photo libraries
                    // (timeline + map endpoints return empty), so no
                    // client-side type check is needed.
                    if (onOpenPhotoExtras != null) {
                        IconButton(onClick = { onOpenPhotoExtras(libraryId) }) {
                            Icon(
                                Icons.Default.Place,
                                contentDescription = "Photo timeline / map",
                            )
                        }
                    }
                },
            )
        },
    ) { padding ->
        PullToRefreshBox(
            // Only show the pull indicator for an actual refresh (items already
            // present); the initial load shows the centered spinner below.
            isRefreshing = ui.loading && ui.items.isNotEmpty(),
            onRefresh = { vm.refresh() },
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            when {
                ui.loading && ui.items.isEmpty() -> LoadingState()
                ui.error != null && ui.items.isEmpty() -> ErrorState(ui.error, onRetry = { vm.refresh() })
                ui.items.isEmpty() -> EmptyState("This library is empty.")
                else -> LazyVerticalGrid(
                    state = gridState,
                    columns = GridCells.Adaptive(120.dp),
                    contentPadding = PaddingValues(12.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    items(ui.items, key = { it.id }) { item ->
                        LibraryGridItem(
                            item = item,
                            serverUrl = ui.serverUrl,
                            // Photos open straight into the swipe-pager viewer;
                            // routing them through ItemDetailScreen triggers a
                            // redirect race.
                            onClick = {
                                if (item.type == "photo") onOpenPhoto(item.id)
                                else onOpenItem(item.id)
                            },
                        )
                    }
                    // Footer spinner while the next page loads.
                    if (ui.loadingMore) {
                        item(span = { GridItemSpan(maxLineSpan) }) {
                            Box(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .padding(16.dp),
                                contentAlignment = Alignment.Center,
                            ) {
                                CircularProgressIndicator()
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun LibraryGridItem(
    item: MediaItem,
    serverUrl: String,
    onClick: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                // Photos are square-ish; movies / shows / albums use a 2:3
                // poster aspect.
                .aspectRatio(if (item.type == "photo") 1f else 2f / 3f)
                .clip(RoundedCornerShape(6.dp))
                .background(MaterialTheme.colorScheme.surfaceVariant),
        ) {
            val art = item.poster_path
            if (!art.isNullOrEmpty() && serverUrl.isNotEmpty()) {
                AsyncImage(
                    model = artworkUrl(serverUrl, art, width = 400),
                    contentDescription = item.title,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.fillMaxSize(),
                )
            } else {
                // Placeholder — first letter of the title.
                Text(
                    text = item.title.firstOrNull()?.uppercase() ?: "?",
                    style = MaterialTheme.typography.headlineMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(top = 24.dp),
                )
            }
        }
        Spacer(Modifier.height(4.dp))
        Text(
            item.title,
            style = MaterialTheme.typography.bodySmall,
            maxLines = 2,
            overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis,
            modifier = Modifier.padding(horizontal = 4.dp),
        )
    }
}
