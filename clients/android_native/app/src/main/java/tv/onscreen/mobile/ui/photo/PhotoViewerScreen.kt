package tv.onscreen.mobile.ui.photo

import android.content.Intent
import android.net.Uri
import android.view.KeyEvent
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
import androidx.compose.foundation.layout.safeDrawingPadding
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
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.unit.LayoutDirection
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import coil.compose.AsyncImage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.model.MediaItem
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.LibraryRepository
import tv.onscreen.mobile.ui.components.EmptyState
import javax.inject.Inject
import tv.onscreen.mobile.ui.input.KeyShortcutHandler
import tv.onscreen.mobile.ui.input.PagerArrows

/** Sibling-resolved photo viewer for the phone client.
 *
 *  Uses HorizontalPager so the standard touch-paginate gesture +
 *  fling work without a custom gesture-detector. Tap toggles the
 *  back-button chrome, double-tap is reserved for a future zoom (not
 *  wired in v1 — pinch-zoom needs `Modifier.transformable` and a
 *  matrix transform we don't need yet for normal browsing).
 *
 *  The tapped photo shows at once; its neighbours load behind it. The
 *  parent album's photos come first. Otherwise they are a window of the
 *  library listing around the photo — at the position the library grid
 *  passes or the server reports (`/items/position`), else found in the
 *  listing's first few pages — extended a page at a time as the user nears
 *  either end. This used to page the whole library (every page at once)
 *  before showing anything, and one failed page failed the viewer. */
@HiltViewModel
class PhotoViewerViewModel @Inject constructor(
    private val itemRepo: ItemRepository,
    private val libraryRepo: LibraryRepository,
    private val serverPrefs: ServerPrefs,
) : ViewModel() {

    private val _state = MutableStateFlow(PhotoViewerUi())
    val state: StateFlow<PhotoViewerUi> = _state.asStateFlow()

    /** The part of the library listing loaded so far: rows [start, end) of
     *  [total]. Null when the neighbours are an album, or none were found. */
    private class Window(val libraryId: String, val watch: String?, var start: Int, var end: Int, val total: Int)

    private var window: Window? = null
    private var extendJob: Job? = null
    private var loadJob: Job? = null

    /** The photo on screen, so a list that grows at the front keeps it. */
    private var currentId: String? = null

    /**
     * Open [initialId]. [position] is its row in the library listing (with
     * the grid's [watch] filter) when the caller knows it — the library
     * grid does; a hub or search tile does not, and the server is asked.
     */
    fun load(initialId: String, position: Int? = null, watch: String? = null) {
        loadJob?.cancel()
        extendJob?.cancel()
        window = null
        currentId = initialId
        loadJob = viewModelScope.launch {
            val serverUrl = serverPrefs.getServerUrl()?.trimEnd('/').orEmpty()
            _state.value = PhotoViewerUi(
                loading = false,
                serverUrl = serverUrl,
                photos = listOf(ViewerPhoto(initialId, position ?: 0)),
            )
            // Neighbours are best-effort: on any failure the photo stays up
            // on its own.
            val detail = try {
                itemRepo.getItem(initialId)
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                return@launch
            }
            val parent = detail.parent_id
            if (!parent.isNullOrEmpty()) {
                val album = try {
                    itemRepo.getChildren(parent).filter { it.type == "photo" }.map { it.id }
                } catch (e: CancellationException) {
                    throw e
                } catch (_: Exception) {
                    emptyList()
                }
                if (album.size >= 2 && initialId in album) {
                    publish(album.mapIndexed { i, id -> ViewerPhoto(id, i) }, total = album.size)
                    return@launch
                }
            }
            // The grid's position, else (a hub, search, a deep link) the
            // server's, for the listing the grid opens on. Without the
            // server's — a 404 from an older server, any failure, or a page
            // that doesn't hold the photo — search the first pages as
            // before. A grid position whose page lacks the photo leaves it
            // on its own, as before.
            val row = position ?: locate(detail.library_id, initialId, watch)
            if (row != null && findInListing(detail.library_id, initialId, row, watch)) return@launch
            if (position == null) findInListing(detail.library_id, initialId, null, watch)
        }
    }

    /** [id]'s row in the listing the library grid opens on (no sort, the
     *  [watch] filter — none, unless a caller passed one), asked of the
     *  server for a photo opened without its position. Null when there is
     *  no library, the server says 404 (the photo isn't in that listing, or
     *  the server predates the endpoint), or the request fails. */
    private suspend fun locate(libraryId: String, id: String, watch: String?): Int? {
        if (libraryId.isEmpty()) return null
        return try {
            libraryRepo.itemPosition(libraryId, id, watch = watch)
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            null
        }
    }

    /** Look for [id] in at most [MAX_SEARCH_PAGES] pages of the listing
     *  (one, centred on [position], when that is known). True when found:
     *  the window is open and published. */
    private suspend fun findInListing(libraryId: String, id: String, position: Int?, watch: String?): Boolean {
        val offsets = if (position != null) {
            listOf((position - PAGE_SIZE / 2).coerceAtLeast(0))
        } else {
            (0 until MAX_SEARCH_PAGES).map { it * PAGE_SIZE }
        }
        for (offset in offsets) {
            val (rows, total) = fetch(libraryId, watch, offset, PAGE_SIZE) ?: return false
            val photos = photosOf(rows, offset)
            if (photos.any { it.id == id }) {
                window = Window(libraryId, watch, start = offset, end = offset + rows.size, total = total)
                publish(photos, total)
                return true
            }
            if (rows.isEmpty() || offset + rows.size >= total) return false
        }
        return false
    }

    /** The pager moved to [id]: near either end of a listing window, load
     *  the next page that way. One page at a time; a failed one is tried
     *  again on a later move. */
    fun onPhotoShown(id: String) {
        currentId = id
        val w = window ?: return
        if (extendJob?.isActive == true) return
        val photos = _state.value.photos
        val i = photos.indexOfFirst { it.id == id }
        if (i < 0) return
        val after = when {
            i >= photos.size - EDGE && w.end < w.total -> true
            i < EDGE && w.start > 0 -> false
            else -> return
        }
        extendJob = viewModelScope.launch {
            val offset = if (after) w.end else (w.start - PAGE_SIZE).coerceAtLeast(0)
            val limit = if (after) PAGE_SIZE else w.start - offset
            val (rows, _) = fetch(w.libraryId, w.watch, offset, limit) ?: return@launch
            val have = _state.value.photos
            val known = have.mapTo(HashSet()) { it.id }
            val more = photosOf(rows, offset).filter { it.id !in known }
            if (after) {
                // An empty page: the listing shrank since; stop here.
                w.end = if (rows.isEmpty()) w.total else offset + rows.size
                publish(have + more, w.total)
            } else {
                w.start = offset
                publish(more + have, w.total)
            }
        }
    }

    private suspend fun fetch(libraryId: String, watch: String?, offset: Int, limit: Int): Pair<List<MediaItem>, Int>? =
        try {
            libraryRepo.getItems(libraryId, limit = limit, offset = offset, watch = watch)
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            null
        }

    private fun photosOf(rows: List<MediaItem>, offset: Int): List<ViewerPhoto> =
        rows.withIndex().filter { it.value.type == "photo" }.map { ViewerPhoto(it.value.id, offset + it.index) }

    /** Show [photos], keeping the one on screen where it is. */
    private fun publish(photos: List<ViewerPhoto>, total: Int) {
        val keep = photos.indexOfFirst { it.id == currentId }.coerceAtLeast(0)
        _state.value = _state.value.copy(photos = photos, startIndex = keep, total = total)
    }

    internal companion object {
        const val PAGE_SIZE = 200

        /** Pages searched for a photo opened without its position. */
        const val MAX_SEARCH_PAGES = 3

        /** How close to an end of the window the next page is fetched. */
        const val EDGE = 10
    }

    /** One-shot EXIF fetch for the currently-visible photo. The
     *  dialog calls this from a `produceState` so the request auto-
     *  cancels if the user dismisses mid-load. Errors return null;
     *  the dialog renders "no EXIF" identically for "missing" and
     *  "fetch failed" since the user can't act differently on
     *  either. */
    suspend fun fetchExif(itemId: String): tv.onscreen.mobile.data.model.PhotoExif? =
        try { itemRepo.getPhotoExif(itemId) } catch (_: Exception) { null }

}

/** A photo in the viewer and its place in the album or library listing. */
data class ViewerPhoto(val id: String, val position: Int)

data class PhotoViewerUi(
    val loading: Boolean = true,
    val serverUrl: String = "",
    val photos: List<ViewerPhoto> = emptyList(),
    /** The page to show: the photo on screen when the list was replaced. */
    val startIndex: Int = 0,
    /** Photos in the album, or rows in the library listing; 0 = not known
     *  (the photo is shown alone). */
    val total: Int = 0,
)

@Composable
fun PhotoViewerScreen(
    itemId: String,
    onBack: () -> Unit,
    /** The photo's row in the library listing, when the caller knows it. */
    position: Int? = null,
    /** That listing's watch filter. */
    watch: String? = null,
    vm: PhotoViewerViewModel = hiltViewModel(),
) {
    LaunchedEffect(itemId) { vm.load(itemId, position, watch) }
    val ui by vm.state.collectAsStateWithLifecycle()

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black),
        contentAlignment = Alignment.Center,
    ) {
        // Saveable: the activity is still recreated on some configuration
        // changes (a language change on Android 12 and older).
        var showExif by rememberSaveable { mutableStateOf(false) }
        var currentId by remember { mutableStateOf<String?>(null) }
        // Chrome (back / info / page counter) visibility. Tapping the
        // photo toggles it so the viewer can go fully immersive — the
        // standard photo-viewer gesture. Starts visible so the user has
        // a way out without guessing the gesture.
        var chromeVisible by rememberSaveable { mutableStateOf(true) }
        when {
            ui.loading -> CircularProgressIndicator(color = Color.White)
            ui.photos.isEmpty() -> EmptyState(stringResource(R.string.photo_none_here))
            else -> PhotoPager(
                ui = ui,
                chromeVisible = chromeVisible,
                onPageChanged = {
                    currentId = it
                    vm.onPhotoShown(it)
                },
                onToggleChrome = { chromeVisible = !chromeVisible },
            )
        }

        // Chrome sits inside the safe area: the app is edge-to-edge, so a
        // bare 8dp inset overlapped Back with the status-bar clock. The photo
        // itself stays full-bleed.
        if (chromeVisible) {
            IconButton(
                onClick = onBack,
                modifier = Modifier.align(Alignment.TopStart).safeDrawingPadding().padding(8.dp),
            ) {
                Icon(
                    Icons.AutoMirrored.Filled.ArrowBack,
                    contentDescription = stringResource(R.string.back),
                    tint = Color.White,
                )
            }
        }
        // Info button — only renders once we have a current photo id
        // (the pager has emitted at least once). Opens an EXIF sheet
        // for the visible image. Hidden along with the rest of the
        // chrome when the user taps to go immersive.
        if (currentId != null && chromeVisible) {
            IconButton(
                onClick = { showExif = true },
                modifier = Modifier.align(Alignment.TopEnd).safeDrawingPadding().padding(8.dp),
            ) {
                Icon(
                    Icons.Default.Info,
                    contentDescription = stringResource(R.string.photo_info),
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
private fun PhotoPager(
    ui: PhotoViewerUi,
    chromeVisible: Boolean,
    onPageChanged: (String) -> Unit,
    onToggleChrome: () -> Unit,
) {
    val pager = rememberPagerState(initialPage = ui.startIndex) { ui.photos.size }

    // The neighbours arrive after the photo, and pages are added at the
    // front as the user goes back: stay on the photo that was showing
    // (the pages are keyed by id; this is the backstop).
    LaunchedEffect(ui.photos) {
        if (pager.currentPage != ui.startIndex && ui.startIndex in ui.photos.indices) {
            pager.scrollToPage(ui.startIndex)
        }
    }

    // Surface the visible page id to the parent so the Info button can
    // open EXIF for the right photo. Re-fires when the user pages.
    LaunchedEffect(pager.currentPage, ui.photos) {
        ui.photos.getOrNull(pager.currentPage)?.let { onPageChanged(it.id) }
    }

    // Keyboard and mouse paging (Chromebooks, tablets): arrow keys (mirrored
    // in a right-to-left layout, like the pager), Page Up / Page Down and
    // Space; PagerArrows adds on-screen buttons on wide windows.
    val scope = rememberCoroutineScope()
    val rtl = LocalLayoutDirection.current == LayoutDirection.Rtl
    fun page(delta: Int) {
        val target = pager.currentPage + delta
        if (target in 0 until pager.pageCount) scope.launch { pager.animateScrollToPage(target) }
    }
    KeyShortcutHandler { e ->
        when (e.keyCode) {
            KeyEvent.KEYCODE_DPAD_LEFT -> { page(if (rtl) 1 else -1); true }
            KeyEvent.KEYCODE_DPAD_RIGHT -> { page(if (rtl) -1 else 1); true }
            KeyEvent.KEYCODE_PAGE_UP -> { page(-1); true }
            KeyEvent.KEYCODE_PAGE_DOWN, KeyEvent.KEYCODE_SPACE -> { page(1); true }
            else -> false
        }
    }

    Box(modifier = Modifier.fillMaxSize()) {
        HorizontalPager(
            state = pager,
            key = { page -> ui.photos[page].id },
            modifier = Modifier.fillMaxSize(),
        ) { page ->
            val photo = ui.photos[page]
            val id = photo.id
            // 1920×1080 fit=contain matches the TV client's viewer —
            // server caches the resize on the first hit so subsequent
            // photos in the same session are warm.
            val url = "${ui.serverUrl}/api/v1/items/$id/image?w=1920&h=1080&fit=contain"
            AsyncImage(
                model = url,
                // The viewer doesn't carry per-photo titles (the sibling
                // list is ids only), so describe by position — better
                // than a null label for TalkBack.
                contentDescription = stringResource(
                    R.string.photo_position,
                    photo.position + 1,
                    ui.total.coerceAtLeast(photo.position + 1),
                ),
                contentScale = ContentScale.Fit,
                modifier = Modifier
                    .fillMaxSize()
                    .pointerInput(Unit) {
                        detectTapGestures(onTap = { onToggleChrome() })
                    },
            )
        }

        val shown = ui.photos.getOrNull(pager.currentPage)
        if (shown != null && ui.total > 1 && chromeVisible) {
            Text(
                // Its place in the whole album or listing, not in the part loaded.
                text = "${shown.position + 1} / ${ui.total}",
                color = Color.White,
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .safeDrawingPadding()
                    .padding(16.dp),
            )
        }
        if (chromeVisible && ui.photos.size > 1) {
            PagerArrows(
                canGoBack = pager.currentPage > 0,
                canGoForward = pager.currentPage < pager.pageCount - 1,
                onBack = { page(-1) },
                onForward = { page(1) },
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
        confirmButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.close)) } },
        title = { Text(stringResource(R.string.photo_info)) },
        text = {
            if (rows.isEmpty()) {
                Text(
                    stringResource(R.string.photo_no_exif),
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
                                stringResource(row.label),
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
                            Text(stringResource(R.string.photo_open_in_maps))

                        }
                    }
                }
            }
        },
    )
}
