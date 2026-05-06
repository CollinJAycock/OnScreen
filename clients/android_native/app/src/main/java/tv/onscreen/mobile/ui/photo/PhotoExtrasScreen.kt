package tv.onscreen.mobile.ui.photo

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.clickable
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
import androidx.compose.material.icons.automirrored.filled.List
import androidx.compose.material.icons.filled.Place
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.PhotoMapPoint
import tv.onscreen.mobile.data.model.PhotoTimelineBucket
import javax.inject.Inject

/**
 * Photo extras: per-library Timeline + Geotagged tabs. Reachable from
 * a photo library's screen via the dedicated route.
 *
 * V1 scope cuts:
 *   - Timeline rows are read-only (count display); tapping a bucket
 *     would ideally filter the library to that month, but that needs
 *     a date-range query on /libraries/{id}/items the server doesn't
 *     yet support. Future iteration.
 *   - Geotagged tab is a flat list with "Open in Maps" tap-handler
 *     per row — no embedded map render. A real map needs MapLibre /
 *     Google Maps Compose, which is its own multi-day add (API key,
 *     Play Services dependency, billing config).
 */
data class PhotoExtrasUi(
    val loading: Boolean = true,
    val timeline: List<PhotoTimelineBucket> = emptyList(),
    val map: List<PhotoMapPoint> = emptyList(),
    val error: String? = null,
)

@HiltViewModel
class PhotoExtrasViewModel @Inject constructor(
    private val api: OnScreenApi,
) : ViewModel() {

    private val _state = MutableStateFlow(PhotoExtrasUi())
    val state: StateFlow<PhotoExtrasUi> = _state.asStateFlow()

    fun load(libraryId: String) {
        viewModelScope.launch {
            _state.value = PhotoExtrasUi(loading = true)
            try {
                val timeline = api.getPhotoTimeline(libraryId).data
                val map = api.getPhotoMap(libraryId).data
                _state.value = PhotoExtrasUi(
                    loading = false,
                    timeline = timeline,
                    map = map,
                )
            } catch (e: Exception) {
                _state.value = PhotoExtrasUi(loading = false, error = e.message)
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PhotoExtrasScreen(
    libraryId: String,
    onOpenItem: (String) -> Unit = {},
    onBack: () -> Unit,
    vm: PhotoExtrasViewModel = hiltViewModel(),
) {
    androidx.compose.runtime.LaunchedEffect(libraryId) { vm.load(libraryId) }
    val ui by vm.state.collectAsState()
    var tab by remember { mutableIntStateOf(0) }
    // Geotagged tab can render as a real OSM map (default) or a flat
    // list (fallback for huge libraries where dropping thousands of
    // markers would tank framerate). Toggle persists for the session.
    var geoAsList by remember { mutableStateOf(false) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Photos") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
                actions = {
                    if (tab == 1 && ui.map.isNotEmpty()) {
                        IconButton(onClick = { geoAsList = !geoAsList }) {
                            Icon(
                                if (geoAsList) Icons.Default.Place
                                    else Icons.AutoMirrored.Filled.List,
                                contentDescription = if (geoAsList) "Show map" else "Show list",
                            )
                        }
                    }
                },
            )
        },
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(padding)) {
            TabRow(selectedTabIndex = tab) {
                Tab(selected = tab == 0, onClick = { tab = 0 }, text = { Text("Timeline") })
                Tab(selected = tab == 1, onClick = { tab = 1 }, text = {
                    Text("Geotagged (${ui.map.size})")
                })
            }
            Box(modifier = Modifier.fillMaxSize()) {
                when {
                    ui.loading -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                    ui.error != null -> Text(
                        "Couldn't load: ${ui.error}",
                        modifier = Modifier.align(Alignment.Center).padding(16.dp),
                    )
                    tab == 0 -> TimelineList(ui.timeline)
                    geoAsList -> GeotaggedList(ui.map)
                    ui.map.isEmpty() -> Box(Modifier.fillMaxSize(), Alignment.Center) {
                        Text(
                            "No geotagged photos in this library.",
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    else -> OsmMap(
                        points = ui.map,
                        onMarkerTap = { p -> onOpenItem(p.id) },
                        modifier = Modifier.fillMaxSize(),
                    )
                }
            }
        }
    }
}

@Composable
private fun TimelineList(buckets: List<PhotoTimelineBucket>) {
    if (buckets.isEmpty()) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Text(
                "No photos with date metadata yet.",
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        return
    }
    val grouped = remember(buckets) { PhotoExtras.groupByYear(buckets) }
    LazyColumn(modifier = Modifier.fillMaxSize().padding(horizontal = 16.dp)) {
        grouped.forEach { (year, monthBuckets) ->
            item(key = "year-$year") {
                Spacer(Modifier.height(12.dp))
                Text(
                    year.toString(),
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                )
                Spacer(Modifier.height(4.dp))
            }
            items(monthBuckets, key = { "${it.year}-${it.month}" }) { b ->
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(vertical = 6.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                ) {
                    Text(PhotoExtras.monthName(b.month), style = MaterialTheme.typography.bodyMedium)
                    Text(
                        PhotoExtras.photosLabel(b.count),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

@Composable
private fun GeotaggedList(points: List<PhotoMapPoint>) {
    if (points.isEmpty()) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Text(
                "No geotagged photos in this library.",
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        return
    }
    val context = LocalContext.current
    LazyColumn(modifier = Modifier.fillMaxSize().padding(horizontal = 16.dp)) {
        items(points, key = { it.id }) { p ->
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable {
                        // Try the geo: scheme; fall back to the
                        // browser if no installed app handles it.
                        val geo = Intent(Intent.ACTION_VIEW,
                            Uri.parse(PhotoExifFormat.mapsGeoUri(p.lat, p.lon))
                        )
                        try { context.startActivity(geo) }
                        catch (_: Exception) {
                            context.startActivity(Intent(Intent.ACTION_VIEW,
                                Uri.parse(PhotoExifFormat.mapsHttpsUrl(p.lat, p.lon))
                            ))
                        }
                    }
                    .padding(vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(
                    Icons.Default.Place,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.width(28.dp),
                )
                Spacer(Modifier.width(8.dp))
                Column(modifier = Modifier.fillMaxWidth()) {
                    Text(
                        PhotoExifFormat.formatGps(p.lat, p.lon),
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    p.taken_at?.takeIf { it.isNotBlank() }?.let {
                        Text(
                            it,
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
    }
}
