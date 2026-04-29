package tv.onscreen.mobile.ui.hub

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bookmarks
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.History
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.CenterAlignedTopAppBar
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import coil.compose.AsyncImage
import tv.onscreen.mobile.data.artworkUrl
import tv.onscreen.mobile.data.model.HubItem
import tv.onscreen.mobile.data.model.Library

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HubScreen(
    onOpenItem: (String) -> Unit,
    onOpenLibrary: (String) -> Unit,
    onOpenSearch: () -> Unit,
    onOpenFavorites: () -> Unit,
    onOpenHistory: () -> Unit,
    onOpenCollections: () -> Unit,
    onOpenDownloads: () -> Unit,
    vm: HubViewModel = hiltViewModel(),
) {
    val ui by vm.state.collectAsState()

    Scaffold(
        topBar = {
            CenterAlignedTopAppBar(
                title = { Text("OnScreen") },
                actions = {
                    IconButton(onClick = onOpenFavorites) {
                        Icon(Icons.Default.Favorite, contentDescription = "Favorites")
                    }
                    IconButton(onClick = onOpenHistory) {
                        Icon(Icons.Default.History, contentDescription = "History")
                    }
                    IconButton(onClick = onOpenCollections) {
                        Icon(Icons.Default.Bookmarks, contentDescription = "Collections")
                    }
                    IconButton(onClick = onOpenDownloads) {
                        Icon(Icons.Default.Download, contentDescription = "Downloads")
                    }
                    IconButton(onClick = onOpenSearch) {
                        Icon(Icons.Default.Search, contentDescription = "Search")
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
                ui.error != null -> Text(
                    "Couldn't load: ${ui.error}",
                    modifier = Modifier
                        .align(Alignment.Center)
                        .padding(16.dp),
                )
                else -> HubBody(
                    ui = ui,
                    serverUrl = ui.serverUrl,
                    onOpenItem = onOpenItem,
                    onOpenLibrary = onOpenLibrary,
                )
            }
        }
    }
}

@Composable
private fun HubBody(
    ui: HubUi,
    serverUrl: String,
    onOpenItem: (String) -> Unit,
    onOpenLibrary: (String) -> Unit,
) {
    val hub = ui.hub ?: return
    // Resolve the three Continue Watching buckets. Newer servers
    // return them pre-split; older servers return only the combined
    // continue_watching feed, which we filter client-side. The
    // null-vs-empty distinction matters: the new fields are nullable
    // on the model so we can detect "older server" and fall back.
    val tv = hub.continue_watching_tv ?: hub.continue_watching.filter { it.type == "episode" }
    val movies = hub.continue_watching_movies ?: hub.continue_watching.filter { it.type == "movie" }
    val other = hub.continue_watching_other
        ?: hub.continue_watching.filter { it.type != "episode" && it.type != "movie" }
    LazyColumn(
        contentPadding = PaddingValues(vertical = 12.dp),
        verticalArrangement = Arrangement.spacedBy(20.dp),
    ) {
        if (tv.isNotEmpty()) {
            item { PosterRow("Continue Watching TV Shows", tv, serverUrl, onOpenItem) }
        }
        if (movies.isNotEmpty()) {
            item { PosterRow("Continue Watching Movies", movies, serverUrl, onOpenItem) }
        }
        if (other.isNotEmpty()) {
            item { PosterRow("Continue Watching", other, serverUrl, onOpenItem) }
        }
        if (hub.recently_added.isNotEmpty()) {
            item { PosterRow("Recently added", hub.recently_added, serverUrl, onOpenItem) }
        }
        if (hub.trending.isNotEmpty()) {
            item { PosterRow("Trending", hub.trending, serverUrl, onOpenItem) }
        }
        hub.recently_added_by_library.forEach { row ->
            if (row.items.isNotEmpty()) {
                item { PosterRow(row.library_name, row.items, serverUrl, onOpenItem) }
            }
        }
        if (ui.libraries.isNotEmpty()) {
            item { LibrariesRow(ui.libraries, onOpenLibrary) }
        }
    }
}

@Composable
private fun PosterRow(
    title: String,
    items: List<HubItem>,
    serverUrl: String,
    onOpenItem: (String) -> Unit,
) {
    Column {
        Text(
            title,
            style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
        )
        LazyRow(
            contentPadding = PaddingValues(horizontal = 16.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            items(items, key = { it.id }) { item ->
                PosterCard(item = item, serverUrl = serverUrl, onClick = { onOpenItem(item.id) })
            }
        }
    }
}

@Composable
private fun PosterCard(item: HubItem, serverUrl: String, onClick: () -> Unit) {
    val art = item.poster_path ?: item.thumb_path
    Column(
        modifier = Modifier
            .width(140.dp)
            .clickable(onClick = onClick),
    ) {
        Surface(
            shape = RoundedCornerShape(8.dp),
            color = MaterialTheme.colorScheme.surfaceVariant,
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(2f / 3f),
        ) {
            if (art != null && serverUrl.isNotEmpty()) {
                AsyncImage(
                    model = artworkUrl(serverUrl, art, width = 400),
                    contentDescription = item.title,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.fillMaxSize(),
                )
            }
        }
        Spacer(Modifier.height(6.dp))
        Text(
            item.title,
            style = MaterialTheme.typography.bodyMedium,
            maxLines = 2,
        )
    }
}

@Composable
private fun LibrariesRow(libraries: List<Library>, onOpenLibrary: (String) -> Unit) {
    Column {
        Text(
            "Libraries",
            style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
        )
        Column(modifier = Modifier.padding(horizontal = 16.dp)) {
            libraries.forEach { lib ->
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable { onOpenLibrary(lib.id) }
                        .padding(vertical = 12.dp),
                ) {
                    Text(lib.name, style = MaterialTheme.typography.bodyLarge)
                    Spacer(Modifier.width(8.dp))
                    Text(
                        lib.type,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}
