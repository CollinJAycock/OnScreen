package tv.onscreen.mobile.ui.series

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
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
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.ui.author.BookCard
import javax.inject.Inject

/** Series detail screen. Lists the audiobooks in the series in
 *  release order — series ascending by year mirrors reading order
 *  most of the time. The scanner doesn't yet emit a series_index
 *  field, so year is the best signal we have for ordering. */
@HiltViewModel
class SeriesViewModel @Inject constructor(
    private val repo: ItemRepository,
    private val serverPrefs: ServerPrefs,
) : ViewModel() {

    private val _state = MutableStateFlow(SeriesUi())
    val state: StateFlow<SeriesUi> = _state.asStateFlow()

    fun load(seriesId: String) {
        viewModelScope.launch {
            _state.value = SeriesUi(loading = true)
            try {
                val detail = repo.getItem(seriesId)
                val children = repo.getChildren(seriesId)
                val books = children
                    .filter { it.type == "audiobook" }
                    .sortedWith(
                        compareBy<ChildItem> { it.year ?: Int.MAX_VALUE }
                            .thenBy { it.title.lowercase() },
                    )
                _state.value = SeriesUi(
                    loading = false,
                    detail = detail,
                    books = books,
                    serverUrl = serverPrefs.getServerUrl()?.trimEnd('/').orEmpty(),
                )
            } catch (e: Exception) {
                _state.value = SeriesUi(loading = false, error = e.message)
            }
        }
    }
}

data class SeriesUi(
    val loading: Boolean = false,
    val detail: ItemDetail? = null,
    val books: List<ChildItem> = emptyList(),
    val serverUrl: String = "",
    val error: String? = null,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SeriesScreen(
    seriesId: String,
    onOpenBook: (String) -> Unit,
    onBack: () -> Unit,
    vm: SeriesViewModel = hiltViewModel(),
) {
    LaunchedEffect(seriesId) { vm.load(seriesId) }
    val ui by vm.state.collectAsState()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(ui.detail?.title ?: "Series") },
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
                ui.loading -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                ui.error != null -> Text(ui.error!!, modifier = Modifier.align(Alignment.Center))
                ui.detail != null -> {
                    val detail = ui.detail!!
                    LazyVerticalGrid(
                        columns = GridCells.Adaptive(120.dp),
                        contentPadding = PaddingValues(16.dp),
                    ) {
                        item(span = { GridItemSpan(maxLineSpan) }) {
                            Column {
                                Text(
                                    detail.title,
                                    style = MaterialTheme.typography.headlineMedium,
                                )
                                Spacer(Modifier.height(4.dp))
                                Text(
                                    "${ui.books.size} ${if (ui.books.size == 1) "book" else "books"}",
                                    style = MaterialTheme.typography.bodyMedium,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                                if (!detail.summary.isNullOrEmpty()) {
                                    Spacer(Modifier.height(12.dp))
                                    Text(
                                        detail.summary,
                                        style = MaterialTheme.typography.bodyMedium,
                                    )
                                }
                                Spacer(Modifier.height(20.dp))
                            }
                        }

                        items(ui.books, key = { it.id }) { b ->
                            BookCard(
                                title = b.title,
                                posterPath = b.poster_path,
                                year = b.year,
                                serverUrl = ui.serverUrl,
                                onClick = { onOpenBook(b.id) },
                            )
                        }
                    }
                }
            }
        }
    }
}
