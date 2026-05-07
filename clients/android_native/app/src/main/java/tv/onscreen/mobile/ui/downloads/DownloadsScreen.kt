package tv.onscreen.mobile.ui.downloads

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CloudOff
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Home
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.downloads.DownloadEntry
import tv.onscreen.mobile.data.downloads.OnScreenDownloadManager
import tv.onscreen.mobile.data.network.ConnectivityObserver
import javax.inject.Inject

@HiltViewModel
class DownloadsViewModel @Inject constructor(
    private val downloads: OnScreenDownloadManager,
    connectivity: ConnectivityObserver,
) : ViewModel() {

    val entries: StateFlow<List<DownloadEntry>> =
        downloads.store.state
            .map { it.entries.sortedByDescending { e -> e.updated_at } }
            .stateIn(viewModelScope, SharingStarted.Eagerly, emptyList())

    val isOnline: StateFlow<Boolean> = connectivity.isOnline

    init {
        viewModelScope.launch { downloads.store.load() }
    }

    fun delete(fileId: String) {
        viewModelScope.launch { downloads.delete(fileId) }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DownloadsScreen(
    onOpenItem: (String) -> Unit,
    onPlay: (String) -> Unit,
    onGoOnline: () -> Unit,
    onBack: () -> Unit,
    vm: DownloadsViewModel = hiltViewModel(),
) {
    val entries by vm.entries.collectAsState()
    val online by vm.isOnline.collectAsState()
    LaunchedEffect(Unit) { /* triggers recomposition on first frame */ }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Downloads") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
                actions = {
                    // "Go online" surfaces only when network has come
                    // back AND the user landed here as the offline-mode
                    // start destination. Tap routes to Hub so they pick
                    // up where the live library begins.
                    if (online) {
                        IconButton(onClick = onGoOnline) {
                            Icon(Icons.Default.Home, contentDescription = "Go to library")
                        }
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
            if (!online) OfflineBanner()
            Box(modifier = Modifier.fillMaxSize()) {
                if (entries.isEmpty()) {
                    Column(
                        modifier = Modifier
                            .align(Alignment.Center)
                            .padding(24.dp),
                        horizontalAlignment = Alignment.CenterHorizontally,
                    ) {
                        Text(
                            "No downloads yet",
                            style = MaterialTheme.typography.titleMedium,
                        )
                        Spacer(Modifier.padding(4.dp))
                        Text(
                            if (online) {
                                "Tap Download on any item to keep it for offline playback."
                            } else {
                                "Connect to the internet to load your library, then download items for offline use."
                            },
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                } else {
                    LazyColumn(contentPadding = PaddingValues(16.dp)) {
                        items(entries, key = { it.file_id }) { e ->
                            DownloadRow(
                                entry = e,
                                online = online,
                                // Completed entries: jump straight to
                                // the player. PlayerViewModel resolves
                                // a synthetic ItemDetail from the
                                // manifest when getItem fails offline.
                                // Non-completed entries open ItemDetail
                                // (only useful when online — needs the
                                // network to retry the download).
                                onOpen = {
                                    if (e.status == "completed") onPlay(e.item_id)
                                    else if (online) onOpenItem(e.item_id)
                                },
                                onDelete = { vm.delete(e.file_id) },
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun OfflineBanner() {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            Icons.Default.CloudOff,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.padding(4.dp))
        Text(
            "Offline — showing your downloaded items",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun DownloadRow(
    entry: DownloadEntry,
    online: Boolean,
    onOpen: () -> Unit,
    onDelete: () -> Unit,
) {
    val playable = entry.status == "completed" || online
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .then(if (playable) Modifier.clickable(onClick = onOpen) else Modifier)
            .padding(vertical = 12.dp),
    ) {
        Column(modifier = Modifier.padding(end = 8.dp)) {
            Text(
                entry.item_title,
                style = MaterialTheme.typography.bodyLarge,
                color = if (playable) MaterialTheme.colorScheme.onSurface
                    else MaterialTheme.colorScheme.onSurfaceVariant,
            )
            val subtitle = when (entry.status) {
                "completed" -> "${humanBytes(entry.size_bytes)} · downloaded"
                "downloading" -> "${(progressRatio(entry) * 100).toInt()}% · ${humanBytes(entry.downloaded_bytes)} of ${humanBytes(entry.size_bytes)}"
                "failed" -> entry.error ?: "Failed"
                "queued" -> "Queued — waiting for network"
                else -> entry.status
            }
            Text(
                subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (entry.status != "completed") {
                Spacer(Modifier.padding(2.dp))
                LinearProgressIndicator(
                    progress = { progressRatio(entry) },
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
        Spacer(Modifier.padding(4.dp))
        IconButton(onClick = onDelete) {
            Icon(Icons.Default.Delete, contentDescription = "Delete download")
        }
    }
}

private fun progressRatio(e: DownloadEntry): Float =
    if (e.size_bytes <= 0) 0f else (e.downloaded_bytes.toFloat() / e.size_bytes.toFloat()).coerceIn(0f, 1f)

private fun humanBytes(n: Long): String {
    if (n < 1024) return "${n}B"
    val units = arrayOf("KB", "MB", "GB", "TB")
    var v = n.toDouble() / 1024.0
    var i = 0
    while (v >= 1024 && i < units.size - 1) { v /= 1024; i++ }
    return "%.1f%s".format(v, units[i])
}
