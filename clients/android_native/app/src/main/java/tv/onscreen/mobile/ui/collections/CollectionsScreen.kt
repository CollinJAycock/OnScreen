package tv.onscreen.mobile.ui.collections

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
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
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.artworkUrl
import tv.onscreen.mobile.data.model.CollectionItem
import tv.onscreen.mobile.data.model.MediaCollection
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.CollectionRepository
import tv.onscreen.mobile.ui.components.EmptyState
import tv.onscreen.mobile.ui.components.ErrorState
import tv.onscreen.mobile.ui.components.LoadingState
import javax.inject.Inject

@HiltViewModel
class CollectionsViewModel @Inject constructor(
    private val repo: CollectionRepository,
    private val prefs: ServerPrefs,
) : ViewModel() {

    private val _state = MutableStateFlow(CollectionsUi())
    val state: StateFlow<CollectionsUi> = _state.asStateFlow()

    init { load() }

    fun load() {
        viewModelScope.launch {
            _state.value = CollectionsUi(loading = true)
            try {
                _state.value = CollectionsUi(
                    loading = false,
                    items = repo.getCollections(),
                    serverUrl = prefs.getServerUrl().orEmpty(),
                )
            } catch (e: Exception) {
                _state.value = CollectionsUi(loading = false, error = e.message)
            }
        }
    }
}

data class CollectionsUi(
    val loading: Boolean = false,
    val items: List<MediaCollection> = emptyList(),
    val serverUrl: String = "",
    val error: String? = null,
)

@HiltViewModel
class CollectionDetailViewModel @Inject constructor(
    private val repo: CollectionRepository,
    private val prefs: ServerPrefs,
) : ViewModel() {

    private val _state = MutableStateFlow(CollectionDetailUi())
    val state: StateFlow<CollectionDetailUi> = _state.asStateFlow()

    private var lastId: String? = null

    fun load(id: String) {
        lastId = id
        viewModelScope.launch {
            _state.value = CollectionDetailUi(loading = true)
            try {
                // Page 0. The old single limit=200 request silently dropped
                // meta.total and every item past the 200th; we now keep total
                // and page the rest in via loadMore().
                val (items, total) = repo.getItems(id, limit = PAGE_SIZE, offset = 0)
                _state.value = CollectionDetailUi(
                    loading = false,
                    items = items,
                    total = total,
                    serverUrl = prefs.getServerUrl().orEmpty(),
                )
            } catch (e: Exception) {
                _state.value = CollectionDetailUi(loading = false, error = e.message)
            }
        }
    }

    /** Append the next page as the list nears its end. No-ops while a page
     *  is loading or everything is loaded. */
    fun loadMore() {
        val s = _state.value
        val id = lastId ?: return
        if (s.loading || s.loadingMore || s.items.size >= s.total) return
        viewModelScope.launch {
            _state.value = _state.value.copy(loadingMore = true, loadMoreFailed = false)
            try {
                val (more, total) = repo.getItems(id, limit = PAGE_SIZE, offset = s.items.size)
                // distinctBy guards against overlapping pages (ties in a
                // non-unique sort); a page that adds nothing new marks the
                // list complete so the scroll trigger can't loop.
                val merged = (_state.value.items + more).distinctBy { it.id }
                _state.value = _state.value.copy(
                    loadingMore = false,
                    items = merged,
                    total = if (merged.size == s.items.size) merged.size else total,
                )
            } catch (_: Exception) {
                // Re-arm via loadMoreFailed so continued scrolling retries
                // instead of stalling permanently (see LibraryScreen).
                _state.value = _state.value.copy(loadingMore = false, loadMoreFailed = true)
            }
        }
    }

    fun reload() {
        lastId?.let { load(it) }
    }

    private companion object {
        const val PAGE_SIZE = 100
    }
}

data class CollectionDetailUi(
    val loading: Boolean = false,
    val loadingMore: Boolean = false,
    /** Re-trigger signal for the screen's load-more effect; see LibraryUi. */
    val loadMoreFailed: Boolean = false,
    val items: List<CollectionItem> = emptyList(),
    val total: Int = 0,
    val serverUrl: String = "",
    val error: String? = null,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CollectionsScreen(
    onOpenCollection: (String) -> Unit,
    onBack: () -> Unit,
    vm: CollectionsViewModel = hiltViewModel(),
) {
    val ui by vm.state.collectAsStateWithLifecycle()
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Collections") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            when {
                ui.loading -> LoadingState()
                ui.error != null -> ErrorState(ui.error, onRetry = { vm.load() })
                ui.items.isEmpty() -> EmptyState("No collections")
                else -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(ui.items, key = { it.id }) { c ->
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .clickable { onOpenCollection(c.id) }
                                .padding(vertical = 8.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            PosterThumb(
                                serverUrl = ui.serverUrl,
                                path = c.poster_path,
                                contentDescription = c.name,
                            )
                            Spacer(Modifier.width(12.dp))
                            Column {
                                Text(c.name, style = MaterialTheme.typography.bodyLarge)
                                val sub = listOfNotNull(c.type, c.genre).joinToString(" · ")
                                if (sub.isNotEmpty()) {
                                    Text(
                                        sub,
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
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CollectionDetailScreen(
    collectionId: String,
    onOpenItem: (String) -> Unit,
    onBack: () -> Unit,
    vm: CollectionDetailViewModel = hiltViewModel(),
) {
    LaunchedEffect(collectionId) { vm.load(collectionId) }
    val ui by vm.state.collectAsStateWithLifecycle()
    val listState = rememberLazyListState()
    // Infinite scroll: fetch the next page as the last visible row nears the
    // end of the loaded set and the server still has more.
    val shouldLoadMore by remember {
        derivedStateOf {
            val last = listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: -1
            ui.items.isNotEmpty() && ui.items.size < ui.total && last >= ui.items.size - 8
        }
    }
    LaunchedEffect(shouldLoadMore, ui.loadMoreFailed) { if (shouldLoadMore) vm.loadMore() }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Collection") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            when {
                ui.loading -> LoadingState()
                ui.error != null -> ErrorState(ui.error, onRetry = { vm.reload() })
                ui.items.isEmpty() -> EmptyState("This collection is empty")
                else -> LazyColumn(state = listState, contentPadding = PaddingValues(16.dp)) {
                    items(ui.items, key = { it.id }) { item ->
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .clickable { onOpenItem(item.id) }
                                .padding(vertical = 8.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            PosterThumb(
                                serverUrl = ui.serverUrl,
                                path = item.poster_path,
                                contentDescription = item.title,
                            )
                            Spacer(Modifier.width(12.dp))
                            Column {
                                Text(item.title, style = MaterialTheme.typography.bodyLarge)
                                Text(
                                    item.type,
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                        }
                    }
                    // Footer spinner while the next page loads.
                    if (ui.loadingMore) {
                        item {
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

/** Small leading poster thumbnail for a list row. Falls back to a
 *  surface-variant placeholder when artwork or the server URL is
 *  missing. */
@Composable
private fun PosterThumb(serverUrl: String, path: String?, contentDescription: String?) {
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
