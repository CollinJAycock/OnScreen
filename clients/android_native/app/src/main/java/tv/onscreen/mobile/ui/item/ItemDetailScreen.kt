package tv.onscreen.mobile.ui.item

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.Downloading
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.FavoriteBorder
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.work.WorkInfo
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.artworkUrl
import tv.onscreen.mobile.data.downloads.DownloadEntry
import tv.onscreen.mobile.data.downloads.DownloadWorker
import tv.onscreen.mobile.data.downloads.OnScreenDownloadManager
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.WatchStateValue
import tv.onscreen.mobile.data.model.WatchStatus
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.FavoritesRepository
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.playback.MusicQueue
import tv.onscreen.mobile.ui.components.ErrorState
import tv.onscreen.mobile.ui.components.LoadingState
import tv.onscreen.mobile.ui.watch.isWatchContainerType
import tv.onscreen.mobile.ui.watch.isWatchLeafType
import tv.onscreen.mobile.ui.watch.leafPlayLabel
import javax.inject.Inject

@HiltViewModel
class ItemDetailViewModel @Inject constructor(
    private val repo: ItemRepository,
    private val downloads: OnScreenDownloadManager,
    private val favorites: FavoritesRepository,
    private val serverPrefs: ServerPrefs,
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

    private var loadJob: Job? = null

    /**
     * Load [itemId], or refresh it in place when the page already shows it
     * (the screen calls this again on return from the player). A refresh
     * keeps the detail, children and album start on screen until the new
     * ones land: resetting to a spinner wiped them, and until they came
     * back the page drew a greyed-out Play and "No playable files".
     */
    fun load(itemId: String) {
        loadJob?.cancel()
        val current = _state.value
        if (current.detail?.id != itemId || current.error != null) {
            _state.value = ItemDetailUi(loading = true, loadSeq = current.loadSeq)
        }
        loadJob = viewModelScope.launch {
            try {
                val detail = repo.getItem(itemId)
                val serverUrl = serverPrefs.getServerUrl()?.trimEnd('/').orEmpty()
                val container = isContainer(detail.type)
                val before = _state.value
                // A new loadSeq on every publish: the screen re-binds the
                // watch state and bookmarks to it.
                _state.value = if (before.detail?.id == itemId) {
                    before.copy(
                        loading = false,
                        detail = detail,
                        serverUrl = serverUrl,
                        childrenLoaded = before.childrenLoaded || !container,
                        loadSeq = before.loadSeq + 1,
                    )
                } else {
                    ItemDetailUi(
                        detail = detail,
                        serverUrl = serverUrl,
                        childrenLoaded = !container,
                        loadSeq = before.loadSeq + 1,
                    )
                }
                // The download manifest, for the Download button. A disk
                // read, so it takes nothing from the fetches below, and it
                // runs on its own: behind them it waited out a slow or
                // offline children fetch, and a refresh that cancelled this
                // load cancelled it too, leaving the button on stale state.
                viewModelScope.launch { downloads.store.load() }
                // Watching-status is best-effort — the detail page is
                // useful even when the server is on an older build that
                // 404s the route. Fetched after the main detail so the
                // page renders without waiting on it.
                refreshWatchStatus(itemId)
                // Children list — tracks under an album, albums under an
                // artist, chapters under an audiobook. Without this list,
                // the user lands on a bare title + Play and has no way to
                // drill into the structure. Fetched straight after the
                // detail because a multi-file audiobook's Play and an
                // album's first track both wait on it. Best-effort: a
                // failure keeps what the page had (nothing, on a first
                // load), and the body shows the leaf-style layout.
                if (container) {
                    val kids = attempt { repo.getChildren(itemId) }
                    if (_state.value.detail?.id != itemId) return@launch
                    _state.value = _state.value.copy(
                        children = kids.getOrElse { _state.value.children },
                        childrenLoaded = true,
                    )
                }
                // Album / artist: no files of their own - Play starts the
                // first track (an artist's first album), and the playback
                // service queues the rest around it (MusicQueue).
                if (MusicQueue.startsFromContainer(detail.type)) {
                    val start = attempt {
                        MusicQueue.playStart(detail.type, _state.value.children) { repo.getChildren(it) }
                    }
                    if (_state.value.detail?.id != itemId) return@launch
                    _state.value = _state.value.copy(
                        playStartId = start.getOrElse { _state.value.playStartId },
                        playStartResolved = true,
                    )
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                val s = _state.value
                _state.value = if (s.detail?.id == itemId) {
                    // The page is up (a refresh that failed, say offline
                    // after playing a download): keep it rather than swap
                    // it for an error screen, with nothing left pending.
                    s.copy(loading = false, childrenLoaded = true, playStartResolved = true)
                } else {
                    ItemDetailUi(loading = false, error = e.message, loadSeq = s.loadSeq)
                }
            }
        }
    }

    /** [block]'s outcome, without swallowing cancellation the way
     *  runCatching does: a load superseded mid-fetch must stop, not write
     *  its failure over the page. */
    private inline fun <T> attempt(block: () -> T): Result<T> = try {
        Result.success(block())
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(e)
    }

    /** Top-level types that have a meaningful children list to
     *  surface on the detail page. Movies + episodes + tracks + photos
     *  are leaves (Play action only). book_author and book_series
     *  redirect to dedicated screens before children would render. */
    private fun isContainer(type: String): Boolean = type in setOf(
        // show / season are not listed: ItemWatchViewModel owns their
        // season → episode lists (with watch state), so fetching them here
        // too would be a duplicate round trip.
        "anime", "album", "artist", "audiobook", "podcast",
    )

    /** Re-pull the watching-status row. Called after a load and after
     *  every set/clear so the dropdown reflects post-write state. */
    private fun refreshWatchStatus(itemId: String) {
        viewModelScope.launch {
            try {
                val s = repo.getWatchStatus(itemId)
                _state.value = _state.value.copy(watchStatus = s)
            } catch (_: Exception) {
                // Old server / network blip — leave the previous value
                // alone. The user can re-enter the screen to retry.
            }
        }
    }

    /**
     * Set the watching status. Optimistic — we flip the local state
     * first so the dropdown reacts immediately, then fire the PUT.
     * On failure we revert.
     */
    fun setWatchStatus(status: WatchStatus) {
        val itemId = _state.value.detail?.id ?: return
        val previous = _state.value.watchStatus
        _state.value = _state.value.copy(watchStatus = status)
        viewModelScope.launch {
            try {
                repo.setWatchStatus(itemId, status)
            } catch (_: Exception) {
                _state.value = _state.value.copy(watchStatus = previous)
            }
        }
    }

    /** Clear the watching-status row. Server is idempotent so we don't
     *  bother with optimistic-on-failure rollback the same way set does
     *  — a concurrent set+clear race is a UX corner the user can fix
     *  by tapping again. */
    fun clearWatchStatus() {
        val itemId = _state.value.detail?.id ?: return
        val previous = _state.value.watchStatus
        _state.value = _state.value.copy(watchStatus = null)
        viewModelScope.launch {
            try {
                repo.clearWatchStatus(itemId)
            } catch (_: Exception) {
                _state.value = _state.value.copy(watchStatus = previous)
            }
        }
    }

    fun startDownload(fileId: String, itemId: String) {
        viewModelScope.launch {
            // Catch + surface in UI state. An uncaught throw here
            // crashes the process (viewModelScope's default handler
            // forwards to the thread's UncaughtExceptionHandler) — and
            // WorkManager.enqueueUniqueWork can throw for a handful of
            // OS-level reasons (foreground-service-type mismatch on
            // Android 14+, missing Hilt worker factory wiring, etc.).
            try {
                val detail = _state.value.detail
                val file = detail?.files?.firstOrNull { it.id == fileId }
                downloads.enqueue(
                    fileId = fileId,
                    itemId = itemId,
                    itemTitle = detail?.title ?: "Download",
                    itemType = detail?.type ?: "movie",
                    container = file?.container,
                    posterPath = detail?.poster_path,
                )
            } catch (e: Exception) {
                android.util.Log.e("ItemDetailVM", "enqueue failed", e)
                _state.value = _state.value.copy(
                    downloadError = e.message ?: "Couldn't start download",
                )
            }
        }
    }

    fun clearDownloadError() {
        _state.value = _state.value.copy(downloadError = null)
    }

    fun deleteDownload(fileId: String) {
        viewModelScope.launch {
            try {
                downloads.delete(fileId)
            } catch (e: Exception) {
                android.util.Log.e("ItemDetailVM", "delete failed", e)
            }
        }
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
    /** Trimmed server origin (no trailing slash). Used to build artwork
     *  URLs for the hero image; empty until [load] resolves. */
    val serverUrl: String = "",
    /** Per-user watching-status row. Null = not yet set, or the server
     *  doesn't expose the route (older build). The dropdown reads this
     *  to highlight the active selection. */
    val watchStatus: WatchStatus? = null,
    /** Children of the active item — seasons under a show, episodes
     *  under a season, tracks under an album, etc. Empty for leaves
     *  and for types we don't drill into here (movies, photos,
     *  book_author/book_series — the last two route to dedicated
     *  screens). */
    val children: List<tv.onscreen.mobile.data.model.ChildItem> = emptyList(),
    /** Whether [children] has answered (either way). True at once for types
     *  that don't fetch children; until then an audiobook's Play waits
     *  instead of reading as "no playable files". */
    val childrenLoaded: Boolean = false,
    /** Album / artist: the track Play starts ([MusicQueue.playStart]); null
     *  until resolved or when there's nothing to play. */
    val playStartId: String? = null,
    val playStartResolved: Boolean = false,
    /** Bumped each time a load publishes a detail, including a refresh of
     *  the same item, so the screen can re-bind what hangs off it. */
    val loadSeq: Int = 0,
    val error: String? = null,
    /** Transient enqueue/delete error from the Download button. The
     *  screen reads this to show a Toast, then calls clearDownloadError
     *  so the same message doesn't fire again on recompose. */
    val downloadError: String? = null,
)

/** UI-friendly snapshot of a single file's download state. Driven by
 *  the manifest; live WorkManager progress is reported via
 *  [DownloadEntry.downloaded_bytes]/size_bytes which the worker
 *  updates as it writes. */
sealed class DownloadButtonState {
    data object NotDownloaded : DownloadButtonState()
    /** Manager has scheduled the work but the WorkManager-level
     *  constraint (Wi-Fi only, network connected) hasn't fired the
     *  worker yet. Distinct from InProgress so the user sees that
     *  the request landed even when bytes haven't started flowing. */
    data object Queued : DownloadButtonState()
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
            "queued" -> Queued
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
        DownloadButtonState.Queued -> OutlinedButton(onClick = onDelete) {
            Icon(Icons.Default.Downloading, contentDescription = null)
            Spacer(Modifier.width(6.dp))
            Text("Queued — Cancel")
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
        DownloadButtonState.Completed -> {
            // Tapping a completed download deletes the on-disk copy —
            // destructive, so confirm first rather than wiping it on a
            // stray tap of what reads as a passive "Downloaded" badge.
            var confirmRemove by remember { mutableStateOf(false) }
            OutlinedButton(onClick = { confirmRemove = true }) {
                Icon(Icons.Default.CheckCircle, contentDescription = null)
                Spacer(Modifier.width(6.dp))
                Text("Downloaded")
            }
            if (confirmRemove) {
                AlertDialog(
                    onDismissRequest = { confirmRemove = false },
                    title = { Text("Remove download?") },
                    text = { Text("This removes the offline copy from your device. You can download it again later.") },
                    confirmButton = {
                        TextButton(onClick = {
                            confirmRemove = false
                            onDelete()
                        }) { Text("Remove") }
                    },
                    dismissButton = {
                        TextButton(onClick = { confirmRemove = false }) { Text("Cancel") }
                    },
                )
            }
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
    /** Play ignoring the resume point (album / artist → first track). */
    onPlayFromStart: (String) -> Unit = onPlay,
    /** Play an item from a given position (an audiobook bookmark: the
     *  book or chapter it's in, and where). */
    onPlayAt: (String, Long) -> Unit,
    onOpenItem: (String) -> Unit,
    onOpenPhoto: (String) -> Unit,
    onOpenAuthor: (String) -> Unit,
    onOpenSeries: (String) -> Unit,
    onOpenBook: (String) -> Unit,
    onBack: () -> Unit,
    vm: ItemDetailViewModel = hiltViewModel(),
    watchVm: ItemWatchViewModel = hiltViewModel(),
    bookmarksVm: AudiobookBookmarksViewModel = hiltViewModel(),
) {
    LaunchedEffect(itemId) { vm.load(itemId) }
    val ui by vm.state.collectAsStateWithLifecycle()

    // Audiobook bookmarks bind to each loaded detail — incl. the refresh on
    // return from the player, which is how one just added there shows up.
    // Keyed on loadSeq rather than the detail itself so a favourite toggle
    // (a detail copy) doesn't re-fetch them.
    val bookmarksUi by bookmarksVm.state.collectAsStateWithLifecycle()
    LaunchedEffect(ui.detail?.id, ui.loadSeq) { ui.detail?.let(bookmarksVm::bind) }

    // Watch state (watched toggle / up-next / episode marks) binds to each
    // loaded detail the same way — the refresh on return from the player
    // is what brings back resume points and marks made there. The refresh
    // keeps the id, so the id alone would no longer re-bind.
    val watchUi by watchVm.state.collectAsStateWithLifecycle()
    LaunchedEffect(ui.detail?.id, ui.loadSeq) { ui.detail?.let(watchVm::bind) }

    // Surface enqueue / delete failures from the Download button as a
    // Toast so the user gets feedback instead of a silent no-op.
    val context = androidx.compose.ui.platform.LocalContext.current
    LaunchedEffect(ui.downloadError) {
        val msg = ui.downloadError ?: return@LaunchedEffect
        android.widget.Toast.makeText(context, msg, android.widget.Toast.LENGTH_LONG).show()
        vm.clearDownloadError()
    }
    LaunchedEffect(watchUi.message) {
        val msg = watchUi.message ?: return@LaunchedEffect
        android.widget.Toast.makeText(context, msg, android.widget.Toast.LENGTH_SHORT).show()
        watchVm.consumeMessage()
    }
    LaunchedEffect(bookmarksVm) {
        bookmarksVm.messages.collect { res ->
            android.widget.Toast.makeText(context, res, android.widget.Toast.LENGTH_SHORT).show()
        }
    }

    // Type-based redirects: photos open straight into the full-screen
    // viewer; book_author + book_series have dedicated screens that
    // render the children list. The shared ItemDetailScreen would
    // just show a title with no useful body for these types since
    // they don't carry a playable file. AppNav wires these callbacks
    // to navigate WITH popUpTo(item) so the destination replaces this
    // detail screen on the back stack — earlier we did navigate-then-
    // popBackStack which raced and popped the just-pushed entry.
    LaunchedEffect(ui.detail?.id, ui.detail?.type) {
        val d = ui.detail ?: return@LaunchedEffect
        when (d.type) {
            "photo" -> onOpenPhoto(d.id)
            "book_author" -> onOpenAuthor(d.id)
            "book_series" -> onOpenSeries(d.id)
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
                        // Report a problem (movies / episodes / shows / seasons).
                        ReportProblemAction(
                            itemId = d.id,
                            itemType = d.type,
                            fileId = d.files.firstOrNull()?.id,
                            itemLabel = d.title,
                        )
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
                ui.loading -> LoadingState()
                ui.error != null -> ErrorState(message = ui.error, onRetry = { vm.load(itemId) })
                ui.detail != null -> {
                    val d = ui.detail!!
                    val downloadStates by vm.downloadState.collectAsStateWithLifecycle()
                    val chapters = d.files.firstOrNull()?.chapters.orEmpty()
                    val showChapters = d.type == "audiobook" && chapters.isNotEmpty()
                    // A multi-disc album's tracks, disc by disc. Display
                    // only: ui.children keeps the server's order, which is
                    // the play order MusicQueue queues.
                    val discs = remember(d.type, ui.children) {
                        if (d.type == "album") albumDiscGroups(ui.children) else emptyList()
                    }
                    // Children list can run long (50-episode anime
                    // seasons, 200-track classical albums) and chapter
                    // tables likewise — render the whole page in a
                    // LazyColumn so only the visible rows compose.
                    LazyColumn(modifier = Modifier.fillMaxSize()) {
                        // Hero art — fanart_path (16:9 backdrop) when
                        // present, falling back to poster_path. Edge-to-
                        // edge at the top of the page; the body content
                        // gets its own horizontal padding below so the
                        // image doesn't sit framed by a thin border.
                        val heroPath = d.fanart_path ?: d.poster_path
                        if (!heroPath.isNullOrEmpty() && ui.serverUrl.isNotEmpty()) {
                            item(key = "hero") {
                                // surfaceVariant placeholder so there's
                                // no blank flash before the poster loads.
                                Box(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .aspectRatio(16f / 9f)
                                        .background(MaterialTheme.colorScheme.surfaceVariant),
                                ) {
                                    coil.compose.AsyncImage(
                                        model = artworkUrl(ui.serverUrl, heroPath, width = 1080),
                                        contentDescription = null,
                                        contentScale = androidx.compose.ui.layout.ContentScale.Crop,
                                        modifier = Modifier.fillMaxSize(),
                                    )
                                }
                            }
                        }
                        item(key = "header") {
                            Column(modifier = Modifier.padding(16.dp)) {
                                Text(d.title, style = MaterialTheme.typography.headlineSmall)
                                if (d.year != null) {
                                    Text(d.year.toString(), style = MaterialTheme.typography.bodyMedium)
                                }
                                Spacer(Modifier.height(16.dp))
                                // What Play resolves to isn't known yet: a
                                // multi-file audiobook plays through its
                                // chapter children, an album / artist through
                                // its first track. Until those answer, Play
                                // waits rather than reading as unplayable.
                                val playPending =
                                    (d.type == "audiobook" && d.files.isEmpty() && !ui.childrenLoaded) ||
                                        (MusicQueue.startsFromContainer(d.type) && !ui.playStartResolved)
                                // Show / season: the up-next button + Mark
                                // all (ItemWatchSections) replace Play —
                                // a container has no files of its own.
                                if (isWatchContainerType(d.type)) {
                                    ContainerWatchHeader(
                                        ui = watchUi,
                                        itemId = itemId,
                                        itemType = d.type,
                                        itemTitle = d.title,
                                        onPlay = onPlay,
                                        onMarkAll = watchVm::markAll,
                                    )
                                } else Row {
                                    // Books route to the dedicated reader
                                    // (CBZ/CBR page-flip or EPUB WebView);
                                    // every other type goes to ExoPlayer.
                                    val isBook = d.type == "book"
                                    // No playable file → no Play action.
                                    // Books still open the reader (their
                                    // bytes are the archive itself, not
                                    // surfaced as a "file"); albums and
                                    // artists play their first track (see
                                    // ItemDetailUi.playStartId); every other
                                    // type with an empty files list would
                                    // hand the player nothing and error,
                                    // so show a disabled affordance + note
                                    // instead.
                                    val hasFile = d.files.isNotEmpty()
                                    val musicStart = ui.playStartId
                                    // The player starts at the resume point
                                    // (PlayerViewModel.prepare reads
                                    // view_offset_ms), so say so. The watch
                                    // VM's copy wins once bound: a mark
                                    // clears the resume point.
                                    val bound = watchUi.itemId == d.id
                                    val leafLabel = leafPlayLabel(
                                        resumeMs = if (bound) watchUi.resumeMs else d.view_offset_ms,
                                        watched = if (bound) watchUi.itemWatched
                                            else d.watch_state == WatchStateValue.WATCHED,
                                    )
                                    if (isBook || hasFile || isMultiFileBook(d, ui.children)) {
                                        // (A multi-file audiobook has no file
                                        // of its own; the player resolves it
                                        // to the chapter to resume.)
                                        val label = if (isBook) "Read" else leafLabel
                                        Button(onClick = {
                                            if (isBook) onOpenBook(itemId) else onPlay(itemId)
                                        }) {
                                            Icon(Icons.Default.PlayArrow, contentDescription = null)
                                            Spacer(Modifier.width(6.dp))
                                            Text(label)
                                        }
                                    } else if (musicStart != null) {
                                        // From 0:00: a partial play of track 1
                                        // leaves a resume point, but "play the
                                        // album" means from the top.
                                        Button(onClick = { onPlayFromStart(musicStart) }) {
                                            Icon(Icons.Default.PlayArrow, contentDescription = null)
                                            Spacer(Modifier.width(6.dp))
                                            Text("Play")
                                        }
                                    } else if (playPending) {
                                        // Same size as the Play it turns into:
                                        // a spinner where the icon goes, and the
                                        // label an audiobook will settle on.
                                        Button(onClick = {}, enabled = false) {
                                            Box(Modifier.size(24.dp), contentAlignment = Alignment.Center) {
                                                CircularProgressIndicator(
                                                    modifier = Modifier.size(18.dp),
                                                    strokeWidth = 2.dp,
                                                )
                                            }
                                            Spacer(Modifier.width(6.dp))
                                            Text(if (d.type == "audiobook") leafLabel else "Play")
                                        }
                                    } else {
                                        Button(onClick = {}, enabled = false) {
                                            Icon(Icons.Default.PlayArrow, contentDescription = null)
                                            Spacer(Modifier.width(6.dp))
                                            Text("Play")
                                        }
                                    }
                                    // Only the first file is downloadable
                                    // from the detail page for now —
                                    // multi-file items (audiobooks with
                                    // chapters) would need a per-file
                                    // picker, scoped out for v1 of offline.
                                    d.files.firstOrNull()?.let { file ->
                                        Spacer(Modifier.width(8.dp))
                                        DownloadButton(
                                            state = downloadStates[file.id] ?: DownloadButtonState.NotDownloaded,
                                            onDownload = { vm.startDownload(file.id, itemId) },
                                            onDelete = { vm.deleteDownload(file.id) },
                                        )
                                    }
                                }
                                // (Albums / artists: only once the first
                                // track lookup came back empty; audiobooks
                                // once their children did.)
                                val hasMusicStart = MusicQueue.startsFromContainer(d.type) && ui.playStartId != null
                                if (d.files.isEmpty() && d.type != "book" && !isWatchContainerType(d.type) &&
                                    !playPending && !hasMusicStart && !isMultiFileBook(d, ui.children)
                                ) {
                                    Spacer(Modifier.height(8.dp))
                                    Text(
                                        "No playable files for this item.",
                                        style = MaterialTheme.typography.bodyMedium,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                                // Movie / episode: watched toggle.
                                if (isWatchLeafType(d.type)) {
                                    Spacer(Modifier.height(8.dp))
                                    LeafWatchedToggle(
                                        watched = watchUi.itemWatched,
                                        busy = watchUi.markBusy,
                                        onToggle = watchVm::toggleItemWatched,
                                    )
                                }
                                // Watching-status picker. Renders for the
                                // types where the v2.2 anime track surfaces
                                // mean a "where am I in this" question is
                                // meaningful — TV show containers and seasons.
                                if (d.type == "show" || d.type == "season" || d.type == "anime") {
                                    Spacer(Modifier.height(16.dp))
                                    WatchStatusPicker(
                                        active = ui.watchStatus,
                                        onPick = vm::setWatchStatus,
                                        onClear = vm::clearWatchStatus,
                                    )
                                }
                                // Audio-quality badges. Hidden when the item
                                // isn't audio-bearing. Static badges, not
                                // chips — they're display-only, never tapped.
                                val audioBadges = AudioQualityBadges.badges(d.files.firstOrNull())
                                if (audioBadges.isNotEmpty()) {
                                    Spacer(Modifier.height(12.dp))
                                    AudioBadgeRow(audioBadges)
                                }
                                if (!d.summary.isNullOrEmpty()) {
                                    Spacer(Modifier.height(16.dp))
                                    Text(d.summary, style = MaterialTheme.typography.bodyMedium)
                                }
                            }
                        }

                        // Audiobook bookmarks — above the chapter list,
                        // which can run to hundreds of rows. Tapping one
                        // plays the book (or the chapter file it's in)
                        // from its position.
                        bookmarkSection(
                            ui = bookmarksUi,
                            chapterOrder = chapterOrderOf(ui.children),
                            onPlay = { b -> onPlayAt(b.item_id, b.position_ms) },
                            onEditNote = { b, note -> bookmarksVm.updateNote(b.id, note) },
                            onDelete = { b -> bookmarksVm.delete(b.id) },
                            onRetry = bookmarksVm::retry,
                        )

                        // Audiobook chapters: m4b / mp3 / flac books
                        // surface their embedded chapter table.
                        if (showChapters) {
                            item(key = "chapters-header") {
                                Column(modifier = Modifier.padding(horizontal = 16.dp)) {
                                    Spacer(Modifier.height(8.dp))
                                    Text("Chapters", style = MaterialTheme.typography.titleMedium)
                                    Spacer(Modifier.height(8.dp))
                                }
                            }
                            itemsIndexed(chapters) { i, c ->
                                Row(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .padding(horizontal = 16.dp, vertical = 6.dp),
                                ) {
                                    Text(
                                        text = "${i + 1}. ${c.title}",
                                        style = MaterialTheme.typography.bodyMedium,
                                        modifier = Modifier.padding(end = 12.dp),
                                    )
                                    Text(
                                        text = formatDuration(c.start_ms),
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                            }
                        }

                        // Children — seasons under a show, episodes
                        // under a season, tracks under an album, etc.
                        // The header noun adapts to the parent type so
                        // the section heading isn't always "Children".
                        // Shows / seasons get the watch-aware season →
                        // episode list instead (ItemWatchSections).
                        if (isWatchContainerType(d.type)) {
                            watchEpisodeSection(
                                ui = watchUi,
                                onSelectSeason = watchVm::selectSeason,
                                onOpenEpisode = onOpenItem,
                                onToggleEpisode = watchVm::toggleEpisodeWatched,
                                onMarkSeason = watchVm::markSeason,
                                onRetry = watchVm::retry,
                            )
                        } else if (ui.children.isNotEmpty()) {
                            item(key = "children-header") {
                                Column(modifier = Modifier.padding(horizontal = 16.dp)) {
                                    Spacer(Modifier.height(16.dp))
                                    Text(
                                        childrenSectionTitle(d.type),
                                        style = MaterialTheme.typography.titleMedium,
                                    )
                                    Spacer(Modifier.height(8.dp))
                                }
                            }
                            if (discs.size > 1) {
                                // Every disc restarts at track 1, so each
                                // disc's run gets a heading.
                                discs.forEachIndexed { i, group ->
                                    item(key = "disc-${group.disc}") {
                                        Text(
                                            stringResource(R.string.disc_heading, group.disc),
                                            style = MaterialTheme.typography.titleSmall,
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                            modifier = Modifier.padding(
                                                start = 16.dp,
                                                end = 16.dp,
                                                top = if (i == 0) 0.dp else 16.dp,
                                            ),
                                        )
                                    }
                                    items(group.tracks, key = { it.id }) { child ->
                                        Box(modifier = Modifier.padding(horizontal = 16.dp)) {
                                            ChildRow(child = child, onClick = { onOpenItem(child.id) })
                                        }
                                    }
                                }
                            } else {
                                items(ui.children, key = { it.id }) { child ->
                                    Box(modifier = Modifier.padding(horizontal = 16.dp)) {
                                        ChildRow(child = child, onClick = { onOpenItem(child.id) })
                                    }
                                }
                            }
                        } else if (!ui.childrenLoaded) {
                            // Only a container is ever waiting on children.
                            item(key = "children-loading") {
                                Box(Modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) {
                                    CircularProgressIndicator()
                                }
                            }
                        }
                        item(key = "bottom-spacer") { Spacer(Modifier.height(16.dp)) }
                    }
                }
            }
        }
    }
}

/**
 * Static, non-interactive audio-quality badges (Hi-Res / 24-96 /
 * ReplayGain). Display-only, so rendered as Surface pills rather than
 * AssistChips that would invite a tap. FlowRow so a long badge set wraps
 * instead of clipping on a narrow phone.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun AudioBadgeRow(labels: List<String>) {
    FlowRow {
        labels.forEach { label ->
            Surface(
                shape = RoundedCornerShape(8.dp),
                color = MaterialTheme.colorScheme.surfaceVariant,
                contentColor = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(end = 6.dp, bottom = 6.dp),
            ) {
                Text(
                    label,
                    style = MaterialTheme.typography.labelMedium,
                    modifier = Modifier.padding(horizontal = 10.dp, vertical = 6.dp),
                )
            }
        }
    }
}

/** A multi-file audiobook: no file of its own, chapters as children. */
private fun isMultiFileBook(
    d: ItemDetail,
    children: List<tv.onscreen.mobile.data.model.ChildItem>,
): Boolean = d.type == "audiobook" && d.files.isEmpty() && children.any { it.type == "audiobook_chapter" }

private fun childrenSectionTitle(parentType: String): String = when (parentType) {
    "show", "anime" -> "Seasons"
    "season" -> "Episodes"
    "album" -> "Tracks"
    "artist" -> "Albums"
    "audiobook" -> "Chapters"
    "podcast" -> "Episodes"
    else -> "Items"
}

@Composable
private fun ChildRow(
    child: tv.onscreen.mobile.data.model.ChildItem,
    onClick: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(vertical = 10.dp),
    ) {
        // index when present (S01E03, track 3) — gives a stable
        // ordering hint even when the title doesn't carry one.
        if (child.index != null) {
            Text(
                text = "${child.index}.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(end = 12.dp).width(40.dp),
            )
        }
        Column(modifier = Modifier.weight(1f, fill = true)) {
            Text(child.title, style = MaterialTheme.typography.bodyLarge)
            if (child.year != null) {
                Text(
                    child.year.toString(),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        if (child.duration_ms != null && child.duration_ms > 0) {
            Text(
                text = formatDuration(child.duration_ms),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/**
 * Five-state watching-status picker. Mirrors the v2.2 server enum:
 * Plan to Watch / Watching / On Hold / Completed / Dropped. The active
 * choice highlights with the primary colour; tapping the active one a
 * second time clears it (idempotent on the server side).
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun WatchStatusPicker(
    active: WatchStatus?,
    onPick: (WatchStatus) -> Unit,
    onClear: () -> Unit,
) {
    Column {
        Text(
            "Watching status",
            style = MaterialTheme.typography.labelLarge,
        )
        Spacer(Modifier.height(4.dp))
        // FlowRow so the five options wrap on narrow phones instead of
        // clipping the trailing ones off-screen.
        FlowRow {
            WatchStatus.values().forEach { s ->
                val isActive = active == s
                TextButton(
                    onClick = { if (isActive) onClear() else onPick(s) },
                ) {
                    // Non-color selection cue: a leading check on the
                    // active option so the choice reads for color-blind
                    // users, not just by the accent tint.
                    if (isActive) {
                        Icon(
                            Icons.Default.Check,
                            contentDescription = "Selected",
                            modifier = Modifier.size(16.dp),
                            tint = MaterialTheme.colorScheme.primary,
                        )
                        Spacer(Modifier.width(4.dp))
                    }
                    Text(
                        text = labelFor(s),
                        color = if (isActive) MaterialTheme.colorScheme.primary
                            else MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

/** Display label for a [WatchStatus]. Lives next to the picker so a
 *  future i18n pass can swap to stringResource without touching the
 *  enum definition. */
private fun labelFor(s: WatchStatus): String = when (s) {
    WatchStatus.PLAN_TO_WATCH -> "Plan"
    WatchStatus.WATCHING -> "Watching"
    WatchStatus.ON_HOLD -> "Hold"
    WatchStatus.COMPLETED -> "Done"
    WatchStatus.DROPPED -> "Dropped"
}
