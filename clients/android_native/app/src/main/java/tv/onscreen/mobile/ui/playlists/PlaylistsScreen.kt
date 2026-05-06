package tv.onscreen.mobile.ui.playlists

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.AssistChip
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel

/**
 * User-owned playlists. v1 phone parity ships:
 *   - list (with Smart vs static badge)
 *   - delete
 *   - create smart playlist (rule-builder dialog)
 *
 * Static-playlist add/remove items is reachable from the item detail
 * page in a future iteration; the missing surface here means a phone
 * user can build smart playlists but can't curate static ones from
 * scratch on the phone alone — a deliberate v1 scope cut.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PlaylistsScreen(
    onBack: () -> Unit,
    vm: PlaylistsViewModel = hiltViewModel(),
) {
    val ui by vm.state.collectAsState()
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Playlists") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
        floatingActionButton = {
            FloatingActionButton(onClick = vm::openCreator) {
                Icon(Icons.Default.Add, contentDescription = "New smart playlist")
            }
        },
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            when {
                ui.loading -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                ui.error != null -> Text(
                    "Couldn't load: ${ui.error}",
                    modifier = Modifier
                        .align(Alignment.Center)
                        .padding(16.dp),
                )
                ui.playlists.isEmpty() -> Text(
                    "No playlists yet. Tap + to create a smart playlist.",
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier
                        .align(Alignment.Center)
                        .padding(16.dp),
                )
                else -> LazyColumn(
                    modifier = Modifier.fillMaxSize().padding(horizontal = 16.dp),
                ) {
                    items(ui.playlists, key = { it.id }) { p ->
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(vertical = 10.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Column(modifier = Modifier.weight(1f)) {
                                Row(verticalAlignment = Alignment.CenterVertically) {
                                    Text(p.name, style = MaterialTheme.typography.titleSmall)
                                    if (p.type == "smart_playlist") {
                                        Spacer(Modifier.width(8.dp))
                                        AssistChip(onClick = {}, label = { Text("Smart") })
                                    }
                                }
                                p.description?.takeIf { it.isNotBlank() }?.let {
                                    Text(
                                        it,
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                            }
                            IconButton(onClick = { vm.delete(p.id) }) {
                                Icon(Icons.Default.Delete, contentDescription = "Delete")
                            }
                        }
                    }
                }
            }
        }
    }

    val draft = ui.draft
    if (draft != null) {
        SmartPlaylistCreator(
            draft = draft,
            actionError = ui.actionError,
            onUpdate = vm::updateDraft,
            onSave = vm::saveDraft,
            onCancel = vm::closeCreator,
        )
    }
}

/** Smart playlist creator. Compact — name + type chips + genres CSV
 *  + 4 numeric fields. The full grammar (year range, rating, limit)
 *  is exposed but optional; "Plan to Watch movies rated 7+" is a
 *  3-tap-and-2-types creation. */
@Composable
private fun SmartPlaylistCreator(
    draft: SmartPlaylistRulesDraft,
    actionError: String?,
    onUpdate: ((SmartPlaylistRulesDraft) -> SmartPlaylistRulesDraft) -> Unit,
    onSave: () -> Unit,
    onCancel: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onCancel,
        confirmButton = { TextButton(onClick = onSave) { Text("Save") } },
        dismissButton = { TextButton(onClick = onCancel) { Text("Cancel") } },
        title = { Text("New smart playlist") },
        text = {
            Column {
                OutlinedTextField(
                    value = draft.name,
                    onValueChange = { v -> onUpdate { it.copy(name = v) } },
                    label = { Text("Name") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.height(8.dp))
                Text("Types", style = MaterialTheme.typography.labelMedium)
                Spacer(Modifier.height(4.dp))
                // FlowRow would wrap nicely; we use a plain Row +
                // horizontal scroll-on-overflow via the chip's natural
                // sizing for v1. 9 entries fit on a phone-portrait
                // screen.
                Row {
                    SMART_PLAYLIST_TYPES.forEach { t ->
                        val isSelected = t in draft.types
                        FilterChip(
                            selected = isSelected,
                            onClick = {
                                onUpdate { d ->
                                    d.copy(
                                        types = if (isSelected) d.types - t else d.types + t,
                                    )
                                }
                            },
                            label = { Text(t) },
                            modifier = Modifier.padding(end = 4.dp),
                        )
                    }
                }
                Spacer(Modifier.height(8.dp))
                OutlinedTextField(
                    value = draft.genresCsv,
                    onValueChange = { v -> onUpdate { it.copy(genresCsv = v) } },
                    label = { Text("Genres (comma-separated)") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.height(8.dp))
                Row {
                    OutlinedTextField(
                        value = draft.yearMin,
                        onValueChange = { v -> onUpdate { it.copy(yearMin = v) } },
                        label = { Text("Year ≥") },
                        singleLine = true,
                        modifier = Modifier.weight(1f).padding(end = 4.dp),
                    )
                    OutlinedTextField(
                        value = draft.yearMax,
                        onValueChange = { v -> onUpdate { it.copy(yearMax = v) } },
                        label = { Text("Year ≤") },
                        singleLine = true,
                        modifier = Modifier.weight(1f).padding(start = 4.dp),
                    )
                }
                Spacer(Modifier.height(8.dp))
                Row {
                    OutlinedTextField(
                        value = draft.ratingMin,
                        onValueChange = { v -> onUpdate { it.copy(ratingMin = v) } },
                        label = { Text("Rating ≥") },
                        singleLine = true,
                        modifier = Modifier.weight(1f).padding(end = 4.dp),
                    )
                    OutlinedTextField(
                        value = draft.limit,
                        onValueChange = { v -> onUpdate { it.copy(limit = v) } },
                        label = { Text("Limit") },
                        singleLine = true,
                        modifier = Modifier.weight(1f).padding(start = 4.dp),
                    )
                }
                if (actionError != null) {
                    Spacer(Modifier.height(8.dp))
                    Text(
                        actionError,
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
                if (SmartPlaylistValidator.isLikelyEmpty(draft)) {
                    Spacer(Modifier.height(4.dp))
                    Text(
                        "Tip: pick at least one type, otherwise the playlist will be empty.",
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
            }
        },
    )
}

