package tv.onscreen.mobile.ui.downloads

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
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
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.downloads.DownloadEntry
import tv.onscreen.mobile.data.downloads.OnScreenDownloadManager
import javax.inject.Inject

@HiltViewModel
class DownloadsViewModel @Inject constructor(
    private val downloads: OnScreenDownloadManager,
) : ViewModel() {

    val entries: StateFlow<List<DownloadEntry>> =
        downloads.store.state
            .map { it.entries.sortedByDescending { e -> e.updated_at } }
            .stateIn(viewModelScope, SharingStarted.Eagerly, emptyList())

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
    onBack: () -> Unit,
    vm: DownloadsViewModel = hiltViewModel(),
) {
    val entries by vm.entries.collectAsState()
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
            )
        },
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
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
                        "Tap Download on any item to keep it for offline playback.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            } else {
                LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(entries, key = { it.file_id }) { e ->
                        DownloadRow(
                            entry = e,
                            onOpen = { onOpenItem(e.item_id) },
                            onDelete = { vm.delete(e.file_id) },
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun DownloadRow(
    entry: DownloadEntry,
    onOpen: () -> Unit,
    onDelete: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onOpen)
            .padding(vertical = 12.dp),
    ) {
        Column(modifier = Modifier.padding(end = 8.dp)) {
            Text(entry.item_title, style = MaterialTheme.typography.bodyLarge)
            val subtitle = when (entry.status) {
                "completed" -> "${humanBytes(entry.size_bytes)} · downloaded"
                "downloading" -> "${(progressRatio(entry) * 100).toInt()}% · ${humanBytes(entry.downloaded_bytes)} of ${humanBytes(entry.size_bytes)}"
                "failed" -> entry.error ?: "Failed"
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
