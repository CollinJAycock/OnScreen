package tv.onscreen.mobile.ui.library

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
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
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Place
import androidx.compose.material.icons.filled.Shuffle
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import coil.compose.AsyncImage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import retrofit2.HttpException
import tv.onscreen.mobile.data.artworkUrl
import tv.onscreen.mobile.data.model.MediaItem
import tv.onscreen.mobile.data.model.RandomLibraryItem
import tv.onscreen.mobile.data.model.WatchFilter
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.LibraryRepository
import tv.onscreen.mobile.ui.components.EmptyState
import tv.onscreen.mobile.ui.components.ErrorState
import tv.onscreen.mobile.ui.components.LoadingState
import tv.onscreen.mobile.ui.item.markErrorMessage
import tv.onscreen.mobile.ui.watch.applyWatchedMark
import tv.onscreen.mobile.ui.watch.canMarkWatched
import tv.onscreen.mobile.ui.watch.cardWatchBadge
import tv.onscreen.mobile.ui.watch.supportsWatchState
import tv.onscreen.mobile.ui.watch.watchMenuActions
import javax.inject.Inject

@HiltViewModel
class LibraryViewModel @Inject constructor(
    private val repo: LibraryRepository,
    private val itemRepo: ItemRepository,
    private val prefs: ServerPrefs,
) : ViewModel() {

    private val _state = MutableStateFlow(LibraryUi())
    val state: StateFlow<LibraryUi> = _state.asStateFlow()

    private var currentLibrary: String? = null

    /** Bumped by every first-page fetch. A page that lands after the
     *  filter changed (or a refresh started) belongs to a stale listing and
     *  is dropped instead of overwriting the new one. */
    private var generation = 0

    /** Items with a mark in flight (a second long-press is ignored). */
    private val marking = mutableSetOf<String>()

    private var quietRefresh: Job? = null

    /** Initial load. Idempotent: re-entering a library that's already loaded
     *  keeps the existing items (and the grid's scroll position) rather than
     *  blanking back to a full-screen spinner on every drill-in/return. */
    fun load(libraryId: String) {
        if (currentLibrary == libraryId && _state.value.items.isNotEmpty()) return
        currentLibrary = libraryId
        if (_state.value.libraryType == null) loadLibraryInfo(libraryId)
        fetchFirstPage(keepItems = false)
    }

    /** Pull-to-refresh: reload page 0 but keep the current items visible under
     *  the refresh indicator (no full-screen spinner flash). */
    fun refresh() = fetchFirstPage(keepItems = true)

    /**
     * Coming back to the grid (from a detail page, the player, or the
     * background): re-pull what's loaded so badges, the mark menu and the
     * filter's membership reflect what was just watched or marked — [load]
     * deliberately keeps the existing items. Quiet: no indicator, and a
     * failure keeps the grid as it is. Re-fetches the whole loaded window,
     * page by page, under the current filter — not just page one — so the
     * grid keeps its length and the scroll position (the grid is keyed by id)
     * survives. Skipped before the first load lands and while any load runs;
     * a filter change or pull-to-refresh meanwhile supersedes it.
     */
    fun refreshQuietly() {
        val libraryId = currentLibrary ?: return
        val s = _state.value
        if (s.loading || s.loadingMore || quietRefresh?.isActive == true) return
        val gen = generation
        val watch = s.watchFilter.wire
        val window = s.items.size
        quietRefresh = viewModelScope.launch {
            try {
                val fresh = mutableListOf<MediaItem>()
                var offset = 0
                var total: Int
                do {
                    val (page, t) = repo.getItems(libraryId, limit = PAGE_SIZE, offset = offset, watch = watch)
                    fresh += page
                    offset += page.size
                    total = t
                } while (page.isNotEmpty() && offset < window && offset < total)
                if (gen != generation) return@launch
                val current = _state.value.items
                // A mark still in flight keeps its optimistic badge; a page
                // loadMore appended while this ran sits past the window.
                val merged = (fresh + current.drop(window))
                    .map { item -> if (item.id in marking) current.firstOrNull { it.id == item.id } ?: item else item }
                    .distinctBy { it.id }
                _state.value = _state.value.copy(
                    items = merged,
                    total = maxOf(total, merged.size),
                    error = null,
                )
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                // Quiet: keep what's on screen.
            }
        }
    }

    /** Name for the title bar + type, which decides whether the watch
     *  filter / Surprise me apply. Best-effort: without it the grid still
     *  loads, just without those controls. */
    private fun loadLibraryInfo(libraryId: String) {
        viewModelScope.launch {
            try {
                val lib = repo.getLibraries().firstOrNull { it.id == libraryId } ?: return@launch
                if (currentLibrary != libraryId) return@launch
                _state.value = _state.value.copy(libraryName = lib.name, libraryType = lib.type)
            } catch (_: Exception) {
            }
        }
    }

    /** Switch the Watch filter (All / Unwatched / In progress / Watched) and
     *  reload from the first page with `?watch=`. */
    fun setWatchFilter(filter: WatchFilter) {
        if (_state.value.watchFilter == filter) return
        // A different listing: the grid starts again from the top
        // ([LibraryUi.queryVersion]).
        _state.value = _state.value.copy(
            watchFilter = filter,
            queryVersion = _state.value.queryVersion + 1,
        )
        fetchFirstPage(keepItems = false)
    }

    private fun fetchFirstPage(keepItems: Boolean) {
        val libraryId = currentLibrary ?: return
        val gen = ++generation
        val watch = _state.value.watchFilter.wire
        viewModelScope.launch {
            _state.value = _state.value.copy(
                loading = true,
                loadingMore = false,
                loadMoreFailed = false,
                error = null,
                items = if (keepItems) _state.value.items else emptyList(),
                total = if (keepItems) _state.value.total else 0,
            )
            try {
                val (items, total) = repo.getItems(libraryId, limit = PAGE_SIZE, offset = 0, watch = watch)
                val serverUrl = prefs.getServerUrl().orEmpty()
                if (gen != generation) return@launch
                _state.value = _state.value.copy(
                    loading = false,
                    items = items,
                    total = total,
                    serverUrl = serverUrl,
                )
            } catch (e: Exception) {
                if (gen != generation) return@launch
                _state.value = _state.value.copy(loading = false, error = e.message)
            }
        }
    }

    /** Append the next page as the grid nears its end. No-ops while a page is
     *  already loading or everything is loaded. */
    fun loadMore() {
        val s = _state.value
        val libraryId = currentLibrary ?: return
        if (s.loading || s.loadingMore || s.items.size >= s.total) return
        val gen = generation
        viewModelScope.launch {
            // Clear loadMoreFailed on start so the screen's retry-arming
            // LaunchedEffect (keyed on it) can fire again after a prior fail.
            _state.value = _state.value.copy(loadingMore = true, loadMoreFailed = false)
            try {
                val (more, total) = repo.getItems(
                    libraryId,
                    limit = PAGE_SIZE,
                    offset = s.items.size,
                    watch = s.watchFilter.wire,
                )
                if (gen != generation) return@launch
                // distinctBy: server pages can overlap when the sort column
                // ties (LIMIT/OFFSET over a non-unique ORDER BY) — a repeated
                // id would crash the grid's stable keys. If a page adds
                // nothing new, treat the list as complete so the scroll
                // trigger can't refetch the same offset forever.
                val merged = (_state.value.items + more).distinctBy { it.id }
                _state.value = _state.value.copy(
                    loadingMore = false,
                    items = merged,
                    total = if (merged.size == s.items.size) merged.size else total,
                )
            } catch (_: Exception) {
                if (gen != generation) return@launch
                // Leave loaded items in place and flip loadMoreFailed so the
                // screen re-arms. Without this signal, shouldLoadMore stayed
                // true (items/total unchanged) but never transitioned, so its
                // LaunchedEffect never re-fired and pagination stalled
                // permanently after a single failed page.
                _state.value = _state.value.copy(loadingMore = false, loadMoreFailed = true)
            }
        }
    }

    /** "Surprise me": a random item under the current watch filter. On a
     *  hit the screen opens it ([LibraryUi.randomPick]); a miss (404) or an
     *  error posts a message instead. */
    fun surpriseMe() {
        val libraryId = currentLibrary ?: return
        if (_state.value.surprising) return
        val filter = _state.value.watchFilter
        _state.value = _state.value.copy(surprising = true)
        viewModelScope.launch {
            try {
                val pick = repo.randomItem(libraryId, watch = filter.wire)
                _state.value = if (pick == null) {
                    _state.value.copy(
                        surprising = false,
                        message = if (filter == WatchFilter.ALL) "Nothing here to pick from yet"
                            else "Nothing matches the ${filter.label} filter",
                    )
                } else {
                    _state.value.copy(surprising = false, randomPick = pick)
                }
            } catch (e: Exception) {
                val msg = if (e is HttpException && e.code() == 501) {
                    "Surprise me isn't available on this server"
                } else {
                    "Couldn't pick something — try again"
                }
                _state.value = _state.value.copy(surprising = false, message = msg)
            }
        }
    }

    /** The screen navigated to [LibraryUi.randomPick]; clear it. */
    fun consumeRandomPick() {
        _state.value = _state.value.copy(randomPick = null)
    }

    fun consumeMessage() {
        _state.value = _state.value.copy(message = null)
    }

    /** Long-press "Mark watched / unwatched" on a grid card. Optimistic:
     *  the badge updates at once and reverts (with a message) on failure. */
    fun setWatched(item: MediaItem, watched: Boolean) {
        if (!canMarkWatched(item.type) || !marking.add(item.id)) return
        replaceItem(applyWatchedMark(item, watched))
        viewModelScope.launch {
            try {
                itemRepo.setWatched(item.id, watched)
                _state.value = _state.value.copy(
                    message = if (watched) "Marked as watched" else "Marked as unwatched",
                )
            } catch (e: Exception) {
                replaceItem(item)
                _state.value = _state.value.copy(message = markErrorMessage(e))
            } finally {
                marking.remove(item.id)
            }
        }
    }

    private fun replaceItem(item: MediaItem) {
        _state.value = _state.value.copy(
            items = _state.value.items.map { if (it.id == item.id) item else it },
        )
    }

    private companion object {
        const val PAGE_SIZE = 100
    }
}

data class LibraryUi(
    val loading: Boolean = false,
    val loadingMore: Boolean = false,
    /** Set when the last loadMore() page threw. Purely a re-trigger signal
     *  for the screen's LaunchedEffect — cleared the moment a load starts. */
    val loadMoreFailed: Boolean = false,
    val items: List<MediaItem> = emptyList(),
    val total: Int = 0,
    /** Server origin needed to build the per-item artwork URL — same pattern
     *  as HubScreen. */
    val serverUrl: String = "",
    val error: String? = null,
    /** From the library list; null until (or unless) it loads. */
    val libraryName: String? = null,
    val libraryType: String? = null,
    val watchFilter: WatchFilter = WatchFilter.ALL,
    /** Bumped each time the query (the watch filter) changes, so the screen
     *  scrolls the grid back to the top for the new listing. Refreshes —
     *  pull-to-refresh, the quiet ON_RESUME re-pull — don't bump it: they
     *  re-list the same query and keep the scroll position. */
    val queryVersion: Int = 0,
    /** A Surprise me pick is in flight. */
    val surprising: Boolean = false,
    /** One-shot: the Surprise me result the screen should open. */
    val randomPick: RandomLibraryItem? = null,
    /** One-shot snackbar text. */
    val message: String? = null,
) {
    /** Watch filter, Surprise me and the mark menu only make sense for
     *  libraries whose items carry a watch state. */
    val watchControls: Boolean get() = supportsWatchState(libraryType)
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LibraryScreen(
    libraryId: String,
    onOpenItem: (String) -> Unit,
    onOpenPhoto: (String) -> Unit,
    onOpenPhotoExtras: ((String) -> Unit)? = null,
    onBack: () -> Unit,
    vm: LibraryViewModel = hiltViewModel(),
) {
    LaunchedEffect(libraryId) { vm.load(libraryId) }
    // Back from a detail page / the player / the background: quietly re-pull
    // so badges, the mark menu and the filter reflect what was just watched
    // or marked. The first ON_RESUME is a no-op — load() is running.
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { vm.refreshQuietly() }
    val ui by vm.state.collectAsStateWithLifecycle()
    val gridState = rememberLazyGridState()
    val snackbar = remember { SnackbarHostState() }

    // New query (filter chip) → back to the top. The grid state outlives the
    // listing (it's hoisted here while the spinner replaces the grid), so
    // without this the new results opened at the old scroll offset, mid-list.
    // The applied version is saveable: returning from a detail page (same
    // query, quiet refresh) must keep the position.
    var appliedQuery by rememberSaveable { mutableIntStateOf(ui.queryVersion) }
    LaunchedEffect(ui.queryVersion) {
        if (ui.queryVersion != appliedQuery) {
            appliedQuery = ui.queryVersion
            gridState.scrollToItem(0)
        }
    }

    // Infinite scroll: when the last visible cell nears the end of the loaded
    // set and the server has more, fetch the next page.
    val shouldLoadMore by remember {
        derivedStateOf {
            val last = gridState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: -1
            ui.items.isNotEmpty() && ui.items.size < ui.total && last >= ui.items.size - 8
        }
    }
    // Key on loadMoreFailed too: after a failed page shouldLoadMore stays
    // true without transitioning, so keying on it alone would never re-fire.
    // The catch flips loadMoreFailed, which re-runs this effect and retries.
    LaunchedEffect(shouldLoadMore, ui.loadMoreFailed) { if (shouldLoadMore) vm.loadMore() }

    // Surprise me landed: open the pick (photos go to the viewer, like a tap).
    LaunchedEffect(ui.randomPick) {
        val pick = ui.randomPick ?: return@LaunchedEffect
        vm.consumeRandomPick()
        if (pick.type == "photo") onOpenPhoto(pick.id) else onOpenItem(pick.id)
    }
    // Show from a composition-scoped coroutine, not inside the effect:
    // consuming the message changes this effect's key, which cancels it —
    // and a showSnackbar running in it — on the very next frame.
    val snackScope = rememberCoroutineScope()
    LaunchedEffect(ui.message) {
        val msg = ui.message ?: return@LaunchedEffect
        vm.consumeMessage()
        snackScope.launch { snackbar.showSnackbar(msg) }
    }

    // Whole-show "unwatched" from the grid wipes every mark and resume point
    // in the show — confirm first (same rule as the show page).
    var confirmUnwatch by remember { mutableStateOf<MediaItem?>(null) }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = {
            TopAppBar(
                title = {
                    Text(
                        ui.libraryName ?: "Library",
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
                actions = {
                    if (ui.watchControls) {
                        IconButton(onClick = vm::surpriseMe, enabled = !ui.surprising) {
                            Icon(Icons.Default.Shuffle, contentDescription = "Surprise me")
                        }
                    }
                    // Photo-extras (timeline / geotagged) entry point. The
                    // extras screen gracefully empties on non-photo libraries
                    // (timeline + map endpoints return empty), so no
                    // client-side type check is needed.
                    if (onOpenPhotoExtras != null) {
                        IconButton(onClick = { onOpenPhotoExtras(libraryId) }) {
                            Icon(
                                Icons.Default.Place,
                                contentDescription = "Photo timeline / map",
                            )
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
            if (ui.watchControls) {
                WatchFilterRow(selected = ui.watchFilter, onSelect = vm::setWatchFilter)
            }
            PullToRefreshBox(
                // Only show the pull indicator for an actual refresh (items already
                // present); the initial load shows the centered spinner below.
                isRefreshing = ui.loading && ui.items.isNotEmpty(),
                onRefresh = { vm.refresh() },
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f),
            ) {
                when {
                    ui.loading && ui.items.isEmpty() -> LoadingState()
                    ui.error != null && ui.items.isEmpty() -> ErrorState(ui.error, onRetry = { vm.refresh() })
                    ui.items.isEmpty() -> EmptyState(
                        if (ui.watchFilter == WatchFilter.ALL) "This library is empty."
                        else "Nothing here matches the ${ui.watchFilter.label} filter.",
                    )
                    else -> LazyVerticalGrid(
                        state = gridState,
                        columns = GridCells.Adaptive(120.dp),
                        contentPadding = PaddingValues(12.dp),
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                        verticalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        items(ui.items, key = { it.id }) { item ->
                            LibraryGridItem(
                                item = item,
                                serverUrl = ui.serverUrl,
                                // Photos open straight into the swipe-pager viewer;
                                // routing them through ItemDetailScreen triggers a
                                // redirect race.
                                onClick = {
                                    if (item.type == "photo") onOpenPhoto(item.id)
                                    else onOpenItem(item.id)
                                },
                                markActions = if (ui.watchControls && canMarkWatched(item.type)) {
                                    watchMenuActions(item)
                                } else {
                                    emptyList()
                                },
                                onMark = { watched ->
                                    if (!watched && item.type == "show") confirmUnwatch = item
                                    else vm.setWatched(item, watched)
                                },
                            )
                        }
                        // Footer spinner while the next page loads.
                        if (ui.loadingMore) {
                            item(span = { GridItemSpan(maxLineSpan) }) {
                                Box(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .padding(16.dp),
                                    contentAlignment = Alignment.Center,
                                ) {
                                    CircularProgressIndicator()
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    confirmUnwatch?.let { show ->
        AlertDialog(
            onDismissRequest = { confirmUnwatch = null },
            title = { Text("Mark all as unwatched?") },
            text = {
                Text(
                    "Every episode of \"${show.title}\" will be marked unwatched. " +
                        "This clears your watched marks and resume points for the whole show.",
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    confirmUnwatch = null
                    vm.setWatched(show, false)
                }) { Text("Mark unwatched") }
            },
            dismissButton = {
                TextButton(onClick = { confirmUnwatch = null }) { Text("Cancel") }
            },
        )
    }
}

/** All / Unwatched / In progress / Watched — sent as `?watch=`. */
@Composable
private fun WatchFilterRow(selected: WatchFilter, onSelect: (WatchFilter) -> Unit) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        modifier = Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = 12.dp),
    ) {
        WatchFilter.entries.forEach { f ->
            FilterChip(
                selected = f == selected,
                onClick = { onSelect(f) },
                label = { Text(f.label) },
            )
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun LibraryGridItem(
    item: MediaItem,
    serverUrl: String,
    onClick: () -> Unit,
    /** Mark actions the long-press menu offers (true = watched); empty =
     *  no menu (non-video libraries, older servers). */
    markActions: List<Boolean>,
    onMark: (Boolean) -> Unit,
) {
    val badge = cardWatchBadge(item)
    var menuOpen by remember { mutableStateOf(false) }
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .combinedClickable(
                onClick = onClick,
                onLongClickLabel = if (markActions.isNotEmpty()) "Watched options" else null,
                onLongClick = if (markActions.isNotEmpty()) {
                    { menuOpen = true }
                } else {
                    null
                },
            ),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                // Photos are square-ish; movies / shows / albums use a 2:3
                // poster aspect.
                .aspectRatio(if (item.type == "photo") 1f else 2f / 3f)
                .clip(RoundedCornerShape(6.dp))
                .background(MaterialTheme.colorScheme.surfaceVariant),
        ) {
            val art = item.poster_path
            if (!art.isNullOrEmpty() && serverUrl.isNotEmpty()) {
                AsyncImage(
                    model = artworkUrl(serverUrl, art, width = 400),
                    contentDescription = item.title,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.fillMaxSize(),
                )
            } else {
                // Placeholder — first letter of the title.
                Text(
                    text = item.title.firstOrNull()?.uppercase() ?: "?",
                    style = MaterialTheme.typography.headlineMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(top = 24.dp),
                )
            }
            if (badge != null) {
                WatchBadge(
                    watched = badge.watched,
                    unwatchedCount = badge.unwatchedCount,
                    label = badge.label,
                    modifier = Modifier
                        .align(Alignment.TopEnd)
                        .padding(4.dp),
                )
                val progress = badge.progress
                if (progress != null) {
                    LinearProgressIndicator(
                        progress = { progress },
                        modifier = Modifier
                            .align(Alignment.BottomCenter)
                            .fillMaxWidth()
                            .height(4.dp)
                            .semantics { contentDescription = badge.label },
                    )
                }
            }
            DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                markActions.forEach { watched ->
                    DropdownMenuItem(
                        text = { Text(if (watched) "Mark watched" else "Mark unwatched") },
                        onClick = {
                            menuOpen = false
                            onMark(watched)
                        },
                    )
                }
            }
        }
        Spacer(Modifier.height(4.dp))
        Text(
            item.title,
            style = MaterialTheme.typography.bodySmall,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(horizontal = 4.dp),
        )
    }
}

/** Top-corner watch badge: a check for watched, a count for a show /
 *  season with episodes left. */
@Composable
private fun WatchBadge(
    watched: Boolean,
    unwatchedCount: Long?,
    label: String,
    modifier: Modifier = Modifier,
) {
    when {
        watched -> Surface(
            shape = CircleShape,
            color = MaterialTheme.colorScheme.primary,
            contentColor = MaterialTheme.colorScheme.onPrimary,
            modifier = modifier.clearAndSetSemantics { contentDescription = label },
        ) {
            Icon(
                Icons.Default.Check,
                contentDescription = null,
                modifier = Modifier
                    .padding(3.dp)
                    .size(14.dp),
            )
        }
        unwatchedCount != null -> Surface(
            shape = RoundedCornerShape(10.dp),
            color = MaterialTheme.colorScheme.primary,
            contentColor = MaterialTheme.colorScheme.onPrimary,
            modifier = modifier.clearAndSetSemantics { contentDescription = label },
        ) {
            Text(
                unwatchedCount.toString(),
                style = MaterialTheme.typography.labelSmall,
                modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp),
            )
        }
    }
}
