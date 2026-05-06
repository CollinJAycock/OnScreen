package tv.onscreen.mobile.ui.discover

import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import coil.compose.AsyncImage
import tv.onscreen.mobile.data.model.DiscoverItem

/**
 * TMDB discover screen — search box + result grid + per-card request
 * button. Closes the matrix's "in-app TMDB discover + request" claim
 * on phone parity.
 *
 * Distinct from the [tv.onscreen.mobile.ui.search.SearchScreen]
 * because the user intent is different: search hits the local
 * library, discover hits TMDB to find things NOT in the library yet.
 * Two surfaces, two endpoints.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DiscoverScreen(
    onBack: () -> Unit,
    vm: DiscoverViewModel = hiltViewModel(),
) {
    val ui by vm.state.collectAsState()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Discover") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .padding(16.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                OutlinedTextField(
                    value = ui.query,
                    onValueChange = vm::onQueryChanged,
                    modifier = Modifier.weight(1f),
                    placeholder = { Text("Find a movie or show…") },
                    singleLine = true,
                )
                Spacer(Modifier.width(8.dp))
                IconButton(onClick = vm::search) {
                    Icon(Icons.Default.Search, contentDescription = "Search TMDB")
                }
            }
            Spacer(Modifier.height(12.dp))

            ui.error?.let { err ->
                Text(
                    err,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(vertical = 8.dp),
                )
            }

            Box(modifier = Modifier.fillMaxSize()) {
                when {
                    ui.loading -> CircularProgressIndicator(
                        Modifier.align(Alignment.Center),
                    )
                    ui.results.isEmpty() && ui.query.isNotBlank() && ui.error == null ->
                        Text(
                            "No results — try a broader title.",
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.align(Alignment.Center),
                        )
                    ui.results.isEmpty() ->
                        Text(
                            "Search TMDB to find titles you can request to add to the library.",
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.align(Alignment.Center),
                        )
                    else -> LazyVerticalGrid(
                        columns = GridCells.Adaptive(minSize = 160.dp),
                        contentPadding = PaddingValues(vertical = 8.dp),
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                        verticalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        items(ui.results, key = { it.tmdb_id }) { item ->
                            DiscoverCard(
                                item = item,
                                submitting = item.tmdb_id in ui.submitting,
                                submitted = item.tmdb_id in ui.submitted,
                                onRequest = { vm.request(item) },
                            )
                        }
                    }
                }
            }
        }
    }
}

/** One result card. Shows poster + title + year + a Request button.
 *  Items already in the library are tagged "In library" and the
 *  Request button greys out — duplicating an existing title with a
 *  request would just queue an arr-side no-op. */
@Composable
private fun DiscoverCard(
    item: DiscoverItem,
    submitting: Boolean,
    submitted: Boolean,
    onRequest: () -> Unit,
) {
    Column(
        modifier = Modifier.fillMaxWidth(),
    ) {
        if (item.poster_url != null) {
            AsyncImage(
                model = item.poster_url,
                contentDescription = item.title,
                modifier = Modifier
                    .fillMaxWidth()
                    .height(240.dp)
                    .clip(RoundedCornerShape(6.dp)),
            )
        } else {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(240.dp)
                    .clip(RoundedCornerShape(6.dp))
                    .padding(8.dp),
            ) {
                Text(
                    text = item.title.take(2).uppercase(),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.headlineLarge,
                    modifier = Modifier.align(Alignment.Center),
                )
            }
        }
        Spacer(Modifier.height(6.dp))
        Text(
            text = item.title,
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.Medium,
            maxLines = 2,
        )
        if (item.year != null) {
            Text(
                text = item.year.toString(),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(4.dp))
        // Three button states: in_library (disabled, "In library"),
        // already requested or submitted-this-session (disabled,
        // "Requested"), default (enabled, "Request").
        val (label, enabled) = when {
            item.in_library -> "In library" to false
            submitted || item.has_active_request -> "Requested" to false
            submitting -> "Sending…" to false
            else -> "Request" to true
        }
        Button(
            onClick = onRequest,
            enabled = enabled,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(label)
        }
    }
}
