package tv.onscreen.mobile.ui.photo

import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
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
            else -> PhotoPager(ui = ui)
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
    }
}

@Composable
private fun PhotoPager(ui: PhotoViewerUi) {
    val pager = rememberPagerState(initialPage = ui.startIndex) { ui.siblingIds.size }

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
