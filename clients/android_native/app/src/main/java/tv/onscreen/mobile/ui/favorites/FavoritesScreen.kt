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
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
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

    private val _state = MutableStateFlow(FavoritesUi())
    val state: StateFlow<FavoritesUi> = _state.asStateFlow()

    init { load() }

    fun load() {
        viewModelScope.launch {
            _state.value = FavoritesUi(loading = true)
            try {
                _state.value = FavoritesUi(
                    loading = false,
                    items = repo.list(limit = 200),
                    serverUrl = prefs.getServerUrl().orEmpty(),
                )
            } catch (e: Exception) {
                _state.value = FavoritesUi(loading = false, error = e.message)
            }
        }
    }
}

data class FavoritesUi(
    val loading: Boolean = false,
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
    val ui by vm.state.collectAsState()
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
                else -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
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
