package tv.onscreen.mobile.ui.library

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
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
) : ViewModel() {

    private val _state = MutableStateFlow(LibraryUi())
    val state: StateFlow<LibraryUi> = _state.asStateFlow()

    fun load(libraryId: String) {
        viewModelScope.launch {
            _state.value = LibraryUi(loading = true)
            try {
                val (items, total) = repo.getItems(libraryId, limit = 200, offset = 0)
                _state.value = LibraryUi(loading = false, items = items, total = total)
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
    val error: String? = null,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LibraryScreen(
    libraryId: String,
    onOpenItem: (String) -> Unit,
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
                ) {
                    items(ui.items, key = { it.id }) { item ->
                        Text(
                            item.title,
                            modifier = Modifier
                                .clickable { onOpenItem(item.id) }
                                .padding(8.dp),
                        )
                    }
                }
            }
        }
    }
}
