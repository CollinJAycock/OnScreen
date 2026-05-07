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
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextAlign
import coil.compose.AsyncImage
import tv.onscreen.mobile.data.artworkUrl
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
import androidx.compose.runtime.getValue
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

    fun load(libraryId: String) {
        viewModelScope.launch {
            _state.value = LibraryUi(loading = true)
            try {
                val (items, total) = repo.getItems(libraryId, limit = 200, offset = 0)
                _state.value = LibraryUi(
                    loading = false,
                    items = items,
                    total = total,
                    serverUrl = prefs.getServerUrl().orEmpty(),
                )
            } catch (e: Exception) {
                _state.value = LibraryUi(loading = false, error = e.message)
            }
        }
    }
}

data class LibraryUi(
    val loading: Boolean = false,
    val items: List<MediaItem> = emptyList(),
    val total: Int = 0,
    /** Server origin needed to build the per-item artwork URL —
     *  same pattern as HubScreen. Without this the grid had only
     *  bare title text, which on a photo library (titles are
     *  filename-derived like "IMG_0001") looked like "nothing
     *  loaded." */
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
                    // Photo-extras (timeline / geotagged) entry point.
                    // Shown on every library — the extras screen
                    // gracefully empties on non-photo libraries since
                    // the timeline + map endpoints just return empty
                    // for those, no client-side type check needed.
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
            isRefreshing = ui.loading,
            onRefresh = { vm.load(libraryId) },
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            when {
                // Initial load before any items — show the centered
                // spinner. PullToRefreshBox's own indicator handles
                // the refresh case (loading + items already present).
                ui.loading && ui.items.isEmpty() ->
                    CircularProgressIndicator(Modifier.align(Alignment.Center))
                ui.error != null && ui.items.isEmpty() ->
                    Text(ui.error!!, modifier = Modifier.align(Alignment.Center))
                else -> LazyVerticalGrid(
                    columns = GridCells.Adaptive(120.dp),
                    contentPadding = PaddingValues(12.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    items(ui.items, key = { it.id }) { item ->
                        LibraryGridItem(
                            item = item,
                            serverUrl = ui.serverUrl,
                            // Photos open straight into the swipe-pager
                            // viewer. Routing them through ItemDetailScreen
                            // first triggers a redirect race that bounces
                            // the user back to the detail page mid-load.
                            onClick = {
                                if (item.type == "photo") onOpenPhoto(item.id)
                                else onOpenItem(item.id)
                            },
                        )
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
                // Photos are square-ish; movies / shows / albums use
                // a 2:3 poster aspect. Photo libraries store the
                // photo as the item itself (no separate poster_path
                // distinct from the image), so the 2:3 crop would
                // chop off the top/bottom. Adapt per type.
                .aspectRatio(if (item.type == "photo") 1f else 2f / 3f)
                .clip(RoundedCornerShape(6.dp))
                .background(Color(0x1FFFFFFF)),
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
                // Placeholder — first letter of the title. Better than
                // a totally blank tile when the server hasn't enriched
                // the item yet (common for freshly-scanned anime
                // shows whose AniList match hasn't landed).
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
            modifier = Modifier.padding(horizontal = 4.dp),
        )
    }
}
