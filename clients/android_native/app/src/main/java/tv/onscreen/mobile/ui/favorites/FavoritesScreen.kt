package tv.onscreen.mobile.ui.favorites

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
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
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
import tv.onscreen.mobile.data.model.FavoriteItem
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.FavoritesRepository
import tv.onscreen.mobile.ui.components.EmptyState
import tv.onscreen.mobile.ui.components.ErrorState
import tv.onscreen.mobile.ui.components.LoadingState
import javax.inject.Inject

@HiltViewModel
class FavoritesViewModel @Inject constructor(
    private val repo: FavoritesRepository,
    private val prefs: ServerPrefs,
) : ViewModel() {

    // Starts in loading=true: the initial fetch is driven by the screen's
    // ON_RESUME effect (fix 7) rather than an init{} block, so this default
    // keeps the first frame on the spinner instead of flashing "No favorites".
    private val _state = MutableStateFlow(FavoritesUi(loading = true))
    val state: StateFlow<FavoritesUi> = _state.asStateFlow()

    /** (Re)load from page 0. Called on every screen resume so unfavoriting
     *  an item from its detail screen is reflected on Back. */
    fun load() {
        viewModelScope.launch {
            _state.value = FavoritesUi(loading = true)
            try {
                // Page 0. The old single limit=200 request never advanced the
                // offset, silently capping the list at 200 favorites.
                val page = repo.list(limit = PAGE_SIZE, offset = 0)
                _state.value = FavoritesUi(
                    loading = false,
                    items = page,
                    // list() returns no total, so a short page is the
                    // end-of-data signal.
                    endReached = page.size < PAGE_SIZE,
                    serverUrl = prefs.getServerUrl().orEmpty(),
                )
            } catch (e: Exception) {
                _state.value = FavoritesUi(loading = false, error = e.message)
            }
        }
    }

    /** Append the next page as the list nears its end. No-ops while a page
     *  is loading or the end has been reached. */
    fun loadMore() {
        val s = _state.value
        if (s.loading || s.loadingMore || s.endReached) return
        viewModelScope.launch {
            _state.value = _state.value.copy(loadingMore = true, loadMoreFailed = false)
            try {
                val more = repo.list(limit = PAGE_SIZE, offset = _state.value.items.size)
                val merged = (_state.value.items + more).distinctBy { it.id }
                _state.value = _state.value.copy(
                    loadingMore = false,
                    items = merged,
                    // End on a short page, or a full page that added nothing
                    // new (defensive against overlapping server pages).
                    endReached = more.size < PAGE_SIZE || merged.size == s.items.size,
                )
            } catch (_: Exception) {
                // Re-arm via loadMoreFailed so continued scrolling retries
                // instead of stalling permanently (see LibraryScreen).
                _state.value = _state.value.copy(loadingMore = false, loadMoreFailed = true)
            }
        }
    }

    private companion object {
        const val PAGE_SIZE = 100
    }
}

data class FavoritesUi(
    val loading: Boolean = false,
    val loadingMore: Boolean = false,
    /** True once a short page confirms there are no more favorites to page. */
    val endReached: Boolean = false,
    /** Re-trigger signal for the screen's load-more effect; see LibraryUi. */
    val loadMoreFailed: Boolean = false,
    val items: List<FavoriteItem> = emptyList(),
    val serverUrl: String = "",
    val error: String? = null,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FavoritesScreen(
    onOpenItem: (String) -> Unit,
    onBack: () -> Unit,
    vm: FavoritesViewModel = hiltViewModel(),
) {
    val ui by vm.state.collectAsStateWithLifecycle()
    // Fix 7: (re)load whenever the screen enters the foreground. The VM is
    // retained across navigation, so unfavoriting an item from its detail
    // screen would otherwise leave it listed here on Back. ON_RESUME also
    // drives the very first load (VM starts in loading=true), so there's no
    // init{} fetch to double up with.
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { vm.load() }

    val listState = rememberLazyListState()
    // Infinite scroll: fetch the next page as the last visible row nears the
    // end of the loaded set and the server still has more.
    val shouldLoadMore by remember {
        derivedStateOf {
            val last = listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: -1
            !ui.endReached && ui.items.isNotEmpty() && last >= ui.items.size - 8
        }
    }
    LaunchedEffect(shouldLoadMore, ui.loadMoreFailed) { if (shouldLoadMore) vm.loadMore() }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Favorites") },
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
                ui.items.isEmpty() -> EmptyState("No favorites yet")
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
                                path = item.poster_path ?: item.thumb_path,
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
