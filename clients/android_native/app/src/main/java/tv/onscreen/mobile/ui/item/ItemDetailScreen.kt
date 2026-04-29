package tv.onscreen.mobile.ui.item

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.Downloading
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.FavoriteBorder
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.LinearProgressIndicator
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
import androidx.work.WorkInfo
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.downloads.DownloadEntry
import tv.onscreen.mobile.data.downloads.DownloadWorker
import tv.onscreen.mobile.data.downloads.OnScreenDownloadManager
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.repository.FavoritesRepository
import tv.onscreen.mobile.data.repository.ItemRepository
import javax.inject.Inject

@HiltViewModel
class ItemDetailViewModel @Inject constructor(
    private val repo: ItemRepository,
    private val downloads: OnScreenDownloadManager,
    private val favorites: FavoritesRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(ItemDetailUi())
    val state: StateFlow<ItemDetailUi> = _state.asStateFlow()

    /** Per-file download state for the currently-loaded item. The
     *  detail page uses this to render the Download / Downloading X% /
     *  Downloaded ✓ button. Combines the persisted manifest entry
     *  with the live WorkManager progress so the user sees byte
     *  counters update in real time. */
    val downloadState: StateFlow<Map<String, DownloadButtonState>> =
        downloads.store.state
            .combine(flowOf(Unit)) { manifest, _ -> manifest }
            .stateIn(viewModelScope, SharingStarted.Eagerly, downloads.store.state.value)
            .let { manifestFlow ->
                MutableStateFlow<Map<String, DownloadButtonState>>(emptyMap()).also { dest ->
                    viewModelScope.launch(Dispatchers.Default) {
                        manifestFlow.collect { manifest ->
                            dest.value = manifest.entries.associate { e ->
                                e.file_id to DownloadButtonState.fromEntry(e)
                            }
                        }
                    }
                }
            }

    fun load(itemId: String) {
        viewModelScope.launch {
            _state.value = ItemDetailUi(loading = true)
            try {
                _state.value = ItemDetailUi(loading = false, detail = repo.getItem(itemId))
                downloads.store.load()
            } catch (e: Exception) {
                _state.value = ItemDetailUi(loading = false, error = e.message)
            }
        }
    }

    fun startDownload(fileId: String, itemId: String) {
        downloads.enqueue(fileId, itemId)
    }

    fun deleteDownload(fileId: String) {
        viewModelScope.launch { downloads.delete(fileId) }
    }

    /** Optimistic toggle. The detail returned from /items already
     *  carries [ItemDetail.is_favorite]; we flip it locally first so
     *  the heart icon reacts immediately, then fire the API call. On
     *  failure we revert — the operation is idempotent on the server
     *  side so a desync between local state and remote is the only
     *  thing to guard against. */
    fun toggleFavorite() {
        val current = _state.value.detail ?: return
        val nextValue = !current.is_favorite
        _state.value = _state.value.copy(detail = current.copy(is_favorite = nextValue))
        viewModelScope.launch {
            try {
                if (nextValue) favorites.add(current.id) else favorites.remove(current.id)
            } catch (_: Exception) {
                _state.value = _state.value.copy(detail = current)
            }
        }
    }
}

data class ItemDetailUi(
    val loading: Boolean = false,
    val detail: ItemDetail? = null,
    val error: String? = null,
)

/** UI-friendly snapshot of a single file's download state. Driven by
 *  the manifest; live WorkManager progress is reported via
 *  [DownloadEntry.downloaded_bytes]/size_bytes which the worker
 *  updates as it writes. */
sealed class DownloadButtonState {
    data object NotDownloaded : DownloadButtonState()
    data class InProgress(val downloadedBytes: Long, val totalBytes: Long) : DownloadButtonState() {
        val ratio: Float
            get() = if (totalBytes <= 0) 0f else (downloadedBytes.toFloat() / totalBytes.toFloat()).coerceIn(0f, 1f)
    }
    data object Completed : DownloadButtonState()
    data class Failed(val message: String?) : DownloadButtonState()

    companion object {
        fun fromEntry(e: DownloadEntry): DownloadButtonState = when (e.status) {
            "completed" -> Completed
            "failed" -> Failed(e.error)
            else -> InProgress(e.downloaded_bytes, e.size_bytes)
        }
    }
}

@Composable
private fun DownloadButton(
    state: DownloadButtonState,
    onDownload: () -> Unit,
    onDelete: () -> Unit,
) {
    when (state) {
        DownloadButtonState.NotDownloaded -> OutlinedButton(onClick = onDownload) {
            Icon(Icons.Default.Download, contentDescription = null)
            Spacer(Modifier.width(6.dp))
            Text("Download")
        }
        is DownloadButtonState.InProgress -> Column {
            OutlinedButton(onClick = onDelete) {
                Icon(Icons.Default.Downloading, contentDescription = null)
                Spacer(Modifier.width(6.dp))
                Text("${(state.ratio * 100).toInt()}% — Cancel")
            }
            Spacer(Modifier.height(4.dp))
            LinearProgressIndicator(
                progress = { state.ratio },
                modifier = Modifier.width(160.dp),
            )
        }
        DownloadButtonState.Completed -> OutlinedButton(onClick = onDelete) {
            Icon(Icons.Default.CheckCircle, contentDescription = null)
            Spacer(Modifier.width(6.dp))
            Text("Downloaded")
        }
        is DownloadButtonState.Failed -> OutlinedButton(onClick = onDownload) {
            Icon(Icons.Default.Close, contentDescription = null)
            Spacer(Modifier.width(6.dp))
            Text("Retry")
        }
    }
}

private fun formatDuration(ms: Long): String {
    val totalSec = ms / 1000
    val h = totalSec / 3600
    val m = (totalSec % 3600) / 60
    val s = totalSec % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, s) else "%d:%02d".format(m, s)
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ItemDetailScreen(
    itemId: String,
    onPlay: (String) -> Unit,
    onOpenItem: (String) -> Unit,
    onOpenPhoto: (String) -> Unit,
    onBack: () -> Unit,
    vm: ItemDetailViewModel = hiltViewModel(),
) {
    LaunchedEffect(itemId) { vm.load(itemId) }
    val ui by vm.state.collectAsState()

    // Photos open straight into the full-screen viewer; the detail
    // page would just be a thumbnail wrapping the same image. Pop the
    // detail off the back stack so Back returns to the source list.
    LaunchedEffect(ui.detail?.id, ui.detail?.type) {
        val d = ui.detail
        if (d != null && d.type == "photo") {
            onOpenPhoto(d.id)
            onBack()
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(ui.detail?.title ?: "") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
                actions = {
                    val d = ui.detail
                    if (d != null) {
                        IconButton(onClick = { vm.toggleFavorite() }) {
                            Icon(
                                imageVector = if (d.is_favorite) Icons.Default.Favorite else Icons.Default.FavoriteBorder,
                                contentDescription = if (d.is_favorite) "Remove from favorites" else "Add to favorites",
                            )
                        }
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
                    val d = ui.detail!!
                    val downloadStates by vm.downloadState.collectAsState()
                    Column(modifier = Modifier.padding(16.dp)) {
                        Text(d.title, style = MaterialTheme.typography.headlineSmall)
                        if (d.year != null) {
                            Text(d.year.toString(), style = MaterialTheme.typography.bodyMedium)
                        }
                        Spacer(Modifier.height(16.dp))
                        Row {
                            Button(onClick = { onPlay(itemId) }) {
                                Icon(Icons.Default.PlayArrow, contentDescription = null)
                                Spacer(Modifier.width(6.dp))
                                Text("Play")
                            }
                            // Only the first file is downloadable from
                            // the detail page for now — multi-file
                            // items (audiobooks with chapters) would
                            // need a per-file picker, scoped out for
                            // v1 of offline.
                            d.files.firstOrNull()?.let { file ->
                                Spacer(Modifier.width(8.dp))
                                DownloadButton(
                                    state = downloadStates[file.id] ?: DownloadButtonState.NotDownloaded,
                                    onDownload = { vm.startDownload(file.id, itemId) },
                                    onDelete = { vm.deleteDownload(file.id) },
                                )
                            }
                        }
                        if (!d.summary.isNullOrEmpty()) {
                            Spacer(Modifier.height(16.dp))
                            Text(d.summary, style = MaterialTheme.typography.bodyMedium)
                        }

                        // Audiobook chapters: m4b / mp3 / flac books
                        // surface their embedded chapter table. Tapping
                        // a chapter starts the player at that chapter's
                        // start_ms. Movies render their chapter list the
                        // same way today on the TV client; we keep the
                        // phone scoped to audiobooks since movie chapter
                        // navigation is a remote-control affordance.
                        val chapters = d.files.firstOrNull()?.chapters.orEmpty()
                        if (d.type == "audiobook" && chapters.isNotEmpty()) {
                            Spacer(Modifier.height(24.dp))
                            Text("Chapters", style = MaterialTheme.typography.titleMedium)
                            Spacer(Modifier.height(8.dp))
                            chapters.forEachIndexed { i, c ->
                                Row(
                                    modifier = Modifier
                                        .fillMaxSize()
                                        .padding(vertical = 6.dp),
                                ) {
                                    Text(
                                        text = "${i + 1}. ${c.title}",
                                        style = MaterialTheme.typography.bodyMedium,
                                        modifier = Modifier
                                            .padding(end = 12.dp),
                                    )
                                    Text(
                                        text = formatDuration(c.start_ms),
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
