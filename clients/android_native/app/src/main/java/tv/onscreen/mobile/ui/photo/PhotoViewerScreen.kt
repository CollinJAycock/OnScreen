package tv.onscreen.mobile.ui.photo

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
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
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.Place
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import coil.compose.AsyncImage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.LibraryRepository
import javax.inject.Inject

/** Sibling-resolved photo viewer for the phone client.
 *
 *  Uses HorizontalPager so the standard touch-paginate gesture +
 *  fling work without a custom gesture-detector. Tap toggles the
 *  back-button chrome, double-tap is reserved for a future zoom (not
 *  wired in v1 — pinch-zoom needs `Modifier.transformable` and a
 *  matrix transform we don't need yet for normal browsing).
 *
 *  Sibling resolution mirrors the TV client's [PhotoViewFragment]: the
 *  parent album's children come first, with a paginated library scan as
 *  the fallback when the photo lands here from a hub or favorites tile
 *  with no album context. */
@HiltViewModel
class PhotoViewerViewModel @Inject constructor(
    private val itemRepo: ItemRepository,
    private val libraryRepo: LibraryRepository,
    private val serverPrefs: ServerPrefs,
) : ViewModel() {

    private val _state = MutableStateFlow(PhotoViewerUi())
    val state: StateFlow<PhotoViewerUi> = _state.asStateFlow()

    /** One-shot EXIF fetch for the currently-visible photo. The
     *  dialog calls this from a `produceState` so the request auto-
     *  cancels if the user dismisses mid-load. Errors return null;
     *  the dialog renders "no EXIF" identically for "missing" and
     *  "fetch failed" since the user can't act differently on
     *  either. */
    suspend fun fetchExif(itemId: String): tv.onscreen.mobile.data.model.PhotoExif? =
        try { itemRepo.getPhotoExif(itemId) } catch (_: Exception) { null }

    fun load(initialId: String) {
        viewModelScope.launch {
            _state.value = PhotoViewerUi(loading = true)
            try {
                val serverUrl = serverPrefs.getServerUrl()?.trimEnd('/').orEmpty()
                val detail = itemRepo.getItem(initialId)
                val photos = mutableListOf<String>()

                val parent = detail.parent_id
                if (!parent.isNullOrEmpty()) {
                    photos += itemRepo.getChildren(parent)
                        .filter { it.type == "photo" }
                        .map { it.id }
                }

                if (photos.size < 2) {
                    photos.clear()
                    val pageSize = 200
                    val (firstPage, total) = libraryRepo.getItems(
                        detail.library_id, limit = pageSize, offset = 0,
                    )
                    val pages = sortedMapOf<Int, List<String>>()
                    pages[0] = firstPage.filter { it.type == "photo" }.map { it.id }
                    if (total > pageSize) {
                        val deferred = (pageSize until total step pageSize).map { off ->
                            async {
                                val (p, _) = libraryRepo.getItems(
                                    detail.library_id, limit = pageSize, offset = off,
                                )
                                off to p.filter { it.type == "photo" }.map { it.id }
                            }
                        }
                        for ((off, ids) in deferred.awaitAll()) pages[off] = ids
                    }
                    for (ids in pages.values) photos += ids
                }

                val ids = if (photos.isEmpty()) listOf(initialId) else photos
                val startIndex = ids.indexOf(initialId).coerceAtLeast(0)
                _state.value = PhotoViewerUi(
                    loading = false,
                    serverUrl = serverUrl,
                    siblingIds = ids,
                    startIndex = startIndex,
                )
            } catch (e: Exception) {
                _state.value = PhotoViewerUi(loading = false, error = e.message)
            }
        }
    }
}

data class PhotoViewerUi(
    val loading: Boolean = true,
    val serverUrl: String = "",
    val siblingIds: List<String> = emptyList(),
    val startIndex: Int = 0,
    val error: String? = null,
)

@Composable
fun PhotoViewerScreen(
    itemId: String,
    onBack: () -> Unit,
    vm: PhotoViewerViewModel = hiltViewModel(),
) {
    LaunchedEffect(itemId) { vm.load(itemId) }
    val ui by vm.state.collectAsState()

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black),
        contentAlignment = Alignment.Center,
    ) {
        var showExif by remember { mutableStateOf(false) }
        var currentId by remember { mutableStateOf<String?>(null) }
        when {
            ui.loading -> CircularProgressIndicator(color = Color.White)
            ui.error != null -> Text(
                text = ui.error!!,
                color = Color.White,
                textAlign = TextAlign.Center,
            )
            ui.siblingIds.isEmpty() -> Text(
                text = "No photos here",
                color = Color.White,
            )
            else -> PhotoPager(ui = ui, onPageChanged = { currentId = it })
        }

        IconButton(
            onClick = onBack,
            modifier = Modifier.align(Alignment.TopStart).padding(8.dp),
        ) {
            Icon(
                Icons.AutoMirrored.Filled.ArrowBack,
                contentDescription = "Back",
                tint = Color.White,
            )
        }
        // Info button — only renders once we have a current photo id
        // (the pager has emitted at least once). Opens an EXIF sheet
        // for the visible image.
        if (currentId != null) {
            IconButton(
                onClick = { showExif = true },
                modifier = Modifier.align(Alignment.TopEnd).padding(8.dp),
            ) {
                Icon(
                    Icons.Default.Info,
                    contentDescription = "Photo info",
                    tint = Color.White,
                )
            }
        }
        val openId = currentId
        if (showExif && openId != null) {
            PhotoExifDialog(
                itemId = openId,
                vm = vm,
                onDismiss = { showExif = false },
            )
        }
    }
}

@Composable
private fun PhotoPager(ui: PhotoViewerUi, onPageChanged: (String) -> Unit) {
    val pager = rememberPagerState(initialPage = ui.startIndex) { ui.siblingIds.size }

    // Surface the visible page id to the parent so the Info button can
    // open EXIF for the right photo. Re-fires when the user pages.
    LaunchedEffect(pager.currentPage, ui.siblingIds) {
        ui.siblingIds.getOrNull(pager.currentPage)?.let(onPageChanged)
    }

    Box(modifier = Modifier.fillMaxSize()) {
        HorizontalPager(
            state = pager,
            modifier = Modifier.fillMaxSize(),
        ) { page ->
            val id = ui.siblingIds[page]
            // 1920×1080 fit=contain matches the TV client's viewer —
            // server caches the resize on the first hit so subsequent
            // photos in the same session are warm.
            val url = "${ui.serverUrl}/api/v1/items/$id/image?w=1920&h=1080&fit=contain"
            AsyncImage(
                model = url,
                contentDescription = null,
                contentScale = ContentScale.Fit,
                modifier = Modifier
                    .fillMaxSize()
                    .pointerInput(Unit) {
                        detectTapGestures(onTap = { /* reserved for chrome toggle */ })
                    },
            )
        }

        if (ui.siblingIds.size > 1) {
            Text(
                text = "${pager.currentPage + 1} / ${ui.siblingIds.size}",
                color = Color.White,
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .padding(16.dp),
            )
        }
    }
}

/**
 * EXIF detail sheet. Fetches metadata for [itemId] on first open and
 * renders the rows from [PhotoExifFormat]. The "Open in Maps" link
 * fires an Android intent with `geo:lat,lon?z=15`; if no app handles
 * it, falls back to a Google Maps web URL via the system browser.
 */
@Composable
private fun PhotoExifDialog(
    itemId: String,
    vm: PhotoViewerViewModel,
    onDismiss: () -> Unit,
) {
    val context = LocalContext.current
    // produceState gives us a one-shot async load tied to the dialog
    // lifecycle — it auto-cancels if the dialog dismisses mid-fetch.
    val exif by produceState<tv.onscreen.mobile.data.model.PhotoExif?>(initialValue = null, key1 = itemId) {
        value = vm.fetchExif(itemId)
    }
    val rows = PhotoExifFormat.rows(exif)
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = onDismiss) { Text("Close") } },
        title = { Text("Photo info") },
        text = {
            if (rows.isEmpty()) {
                Text(
                    "No EXIF metadata for this photo.",
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            } else {
                Column {
                    rows.forEach { row ->
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(vertical = 4.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Text(
                                row.label,
                                style = MaterialTheme.typography.labelMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.width(100.dp),
                            )
                            Text(row.value, style = MaterialTheme.typography.bodyMedium)
                        }
                    }
                    val gpsLat = exif?.gps_lat
                    val gpsLon = exif?.gps_lon
                    if (gpsLat != null && gpsLon != null) {
                        Spacer(Modifier.height(8.dp))
                        TextButton(onClick = {
                            val geo = Intent(Intent.ACTION_VIEW,
                                Uri.parse(PhotoExifFormat.mapsGeoUri(gpsLat, gpsLon))
                            )
                            // Try the geo: intent first; fall back to
                            // the maps web URL when no installed app
                            // handles it (rare but possible on a
                            // stripped device).
                            try {
                                context.startActivity(geo)
                            } catch (_: Exception) {
                                context.startActivity(Intent(Intent.ACTION_VIEW,
                                    Uri.parse(PhotoExifFormat.mapsHttpsUrl(gpsLat, gpsLon))
                                ))
                            }
                        }) {
                            Icon(Icons.Default.Place, contentDescription = null)
                            Spacer(Modifier.width(4.dp))
                            Text("Open in Maps")
                        }
                    }
                }
            }
        },
    )
}
