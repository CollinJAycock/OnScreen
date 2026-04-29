package tv.onscreen.mobile.ui.author

import androidx.compose.foundation.clickable
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
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import javax.inject.Inject

/** Author detail screen. Mirrors the web client's /authors/{id} page:
 *  hero with the author portrait, then a Series row + a Books row.
 *  The children of an author are book_series rows (for series-organized
 *  books) and audiobook rows (for standalone books); render them as
 *  separate sections so the structure is visible. */
@HiltViewModel
class AuthorViewModel @Inject constructor(
    private val repo: ItemRepository,
    private val serverPrefs: ServerPrefs,
) : ViewModel() {

    private val _state = MutableStateFlow(AuthorUi())
    val state: StateFlow<AuthorUi> = _state.asStateFlow()

    fun load(authorId: String) {
        viewModelScope.launch {
            _state.value = AuthorUi(loading = true)
            try {
                val detail = repo.getItem(authorId)
                val children = repo.getChildren(authorId)
                val series = children
                    .filter { it.type == "book_series" }
                    .sortedBy { it.title.lowercase() }
                // Year-desc on standalone books mirrors the web ordering;
                // year nulls sink because most modern catalogs tag year
                // and an untagged book is the outlier worth flagging
                // visually by being last.
                val books = children
                    .filter { it.type == "audiobook" }
                    .sortedWith(
                        compareByDescending<ChildItem> { it.year ?: -1 }
                            .thenBy { it.title.lowercase() },
                    )
                _state.value = AuthorUi(
                    loading = false,
                    detail = detail,
                    series = series,
                    books = books,
                    serverUrl = serverPrefs.getServerUrl()?.trimEnd('/').orEmpty(),
                )
            } catch (e: Exception) {
                _state.value = AuthorUi(loading = false, error = e.message)
            }
        }
    }
}

data class AuthorUi(
    val loading: Boolean = false,
    val detail: ItemDetail? = null,
    val series: List<ChildItem> = emptyList(),
    val books: List<ChildItem> = emptyList(),
    val serverUrl: String = "",
    val error: String? = null,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AuthorScreen(
    authorId: String,
    onOpenSeries: (String) -> Unit,
    onOpenBook: (String) -> Unit,
    onBack: () -> Unit,
    vm: AuthorViewModel = hiltViewModel(),
) {
    LaunchedEffect(authorId) { vm.load(authorId) }
    val ui by vm.state.collectAsState()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(ui.detail?.title ?: "Author") },
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
                ui.detail != null -> AuthorContent(
                    ui = ui,
                    onOpenSeries = onOpenSeries,
                    onOpenBook = onOpenBook,
                )
            }
        }
    }
}

@Composable
private fun AuthorContent(
    ui: AuthorUi,
    onOpenSeries: (String) -> Unit,
    onOpenBook: (String) -> Unit,
) {
    LazyVerticalGrid(
        columns = GridCells.Adaptive(120.dp),
        contentPadding = PaddingValues(16.dp),
    ) {
        // Series + standalone books occupy the same grid; section
        // headers separate them. SpanIndex 1 keeps the headers + meta
        // rows full-width while the cards flow as columns.
        item(span = { androidx.compose.foundation.lazy.grid.GridItemSpan(maxLineSpan) }) {
            Column {
                Text(
                    ui.detail!!.title,
                    style = MaterialTheme.typography.headlineMedium,
                )
                val parts = buildList {
                    if (ui.series.isNotEmpty()) add("${ui.series.size} series")
                    if (ui.books.isNotEmpty()) add("${ui.books.size} books")
                }
                if (parts.isNotEmpty()) {
                    Spacer(Modifier.height(4.dp))
                    Text(
                        parts.joinToString(" · "),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (!ui.detail.summary.isNullOrEmpty()) {
                    Spacer(Modifier.height(12.dp))
                    Text(ui.detail.summary, style = MaterialTheme.typography.bodyMedium)
                }
                Spacer(Modifier.height(20.dp))
            }
        }

        if (ui.series.isNotEmpty()) {
            item(span = { androidx.compose.foundation.lazy.grid.GridItemSpan(maxLineSpan) }) {
                SectionHeader("Series")
            }
            items(ui.series, key = { it.id }) { s ->
                BookCard(
                    title = s.title,
                    posterPath = s.poster_path,
                    serverUrl = ui.serverUrl,
                    onClick = { onOpenSeries(s.id) },
                )
            }
        }

        if (ui.books.isNotEmpty()) {
            item(span = { androidx.compose.foundation.lazy.grid.GridItemSpan(maxLineSpan) }) {
                SectionHeader("Books")
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

@Composable
private fun SectionHeader(label: String) {
    Column(modifier = Modifier.padding(top = 8.dp, bottom = 8.dp)) {
        Text(label, style = MaterialTheme.typography.titleMedium)
    }
}

@Composable
fun BookCard(
    title: String,
    posterPath: String?,
    year: Int? = null,
    serverUrl: String,
    onClick: () -> Unit,
) {
    Column(
        modifier = Modifier
            .padding(4.dp)
            .clickable(onClick = onClick),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(2f / 3f),
        ) {
            if (!posterPath.isNullOrEmpty() && serverUrl.isNotEmpty()) {
                AsyncImage(
                    model = artworkUrl(serverUrl, posterPath, width = 320),
                    contentDescription = title,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.fillMaxSize(),
                )
            }
        }
        Spacer(Modifier.height(6.dp))
        Text(title, style = MaterialTheme.typography.bodyMedium, maxLines = 2)
        if (year != null) {
            Text(
                year.toString(),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
