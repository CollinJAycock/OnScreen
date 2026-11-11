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
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Place
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Shuffle
import androidx.compose.material3.AlertDialog
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
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
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
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.job
import kotlinx.coroutines.launch
import retrofit2.HttpException
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.artworkUrl
import tv.onscreen.mobile.data.model.LibraryLetter
import tv.onscreen.mobile.data.model.MediaItem
import tv.onscreen.mobile.data.model.RandomLibraryItem
import tv.onscreen.mobile.data.model.WatchFilter
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.LibraryRepository
import tv.onscreen.mobile.i18n.UiText
import tv.onscreen.mobile.i18n.asString
import tv.onscreen.mobile.ui.components.EmptyState
import tv.onscreen.mobile.ui.components.ErrorState
import tv.onscreen.mobile.ui.components.LoadingState
import tv.onscreen.mobile.ui.item.markErrorText
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

    /** Page fetches in flight, by page index. */
    private val pageJobs = mutableMapOf<Int, Job>()

    /** Automatic retries spent per page since it was last wanted afresh. */
    private val pageRetries = mutableMapOf<Int, Int>()

    /** The rows the grid last reported on screen ([onVisibleRows]). Kept so
     *  a page that lands — or a listing that's replaced — can top up what the
     *  grid is showing without waiting for the next scroll. */
    private var visibleRows: IntRange = 0..0

    /** Initial load. Idempotent: re-entering a library that's already loaded
     *  keeps the existing items (and the grid's scroll position) rather than
     *  blanking back to a full-screen spinner on every drill-in/return. */
    fun load(libraryId: String) {
        if (currentLibrary == libraryId && _state.value.total > 0) return
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
     * failure keeps the grid as it is. Re-fetches the pages on screen (and
     * the margin around them) under the current filter; the grid keeps its
     * length and scroll position, and pages further away go back to
     * placeholders that reload, fresh, when scrolled to — after jumping
     * around a big library, re-pulling every page ever loaded would be dozens
     * of requests per return. Skipped before the first load lands and while
     * it runs; a filter change or pull-to-refresh meanwhile supersedes it.
     * The letter index is re-pulled too: a membership change moves the
     * offsets.
     */
    fun refreshQuietly() {
        val libraryId = currentLibrary ?: return
        val s = _state.value
        if (s.loading || s.total == 0 || quietRefresh?.isActive == true) return
        val gen = generation
        val watch = s.watchFilter.wire
        val before = s.pages.loadedPages
        val onScreen = s.pages.pagesFor(visibleRows.first - PREFETCH_ROWS, visibleRows.last + PREFETCH_ROWS)
        quietRefresh = viewModelScope.launch {
            try {
                var fresh = LibraryPages(s.total, pageSize = PAGE_SIZE)
                for (page in onScreen) {
                    if (page * PAGE_SIZE >= fresh.total && page > 0) break
                    val (rows, total) = repo.getItems(
                        libraryId, limit = PAGE_SIZE, offset = page * PAGE_SIZE, watch = watch,
                    )
                    fresh = fresh.withPage(page, rows, total)
                }
                if (gen != generation) return@launch
                val current = _state.value.pages
                // A mark still in flight keeps its optimistic badge; a page
                // the grid loaded while this ran is kept (it's as fresh).
                val loadedMeanwhile = current.retainPages { it !in before }
                val merged = fresh
                    .mapItems { item -> if (item.id in marking) current.find(item.id) ?: item else item }
                    .plusMissingFrom(loadedMeanwhile)
                _state.value = _state.value.copy(pages = merged, error = null)
                fetchLetters(libraryId, gen, watch)
                ensurePages()
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
        cancelPageFetches()
        val watch = _state.value.watchFilter.wire
        if (!keepItems) {
            // A new listing opens at the top (the screen scrolls there).
            visibleRows = 0..(visibleRows.last - visibleRows.first).coerceAtLeast(0)
        }
        _state.value = _state.value.copy(
            loading = true,
            error = null,
            pages = if (keepItems) _state.value.pages else LibraryPages(pageSize = PAGE_SIZE),
            // The old listing's offsets don't describe a new one.
            letters = if (keepItems) _state.value.letters else emptyList(),
        )
        fetchLetters(libraryId, gen, watch)
        viewModelScope.launch {
            try {
                val (items, total) = repo.getItems(libraryId, limit = PAGE_SIZE, offset = 0, watch = watch)
                val serverUrl = prefs.getServerUrl().orEmpty()
                if (gen != generation) return@launch
                _state.value = _state.value.copy(
                    loading = false,
                    // Only page one is known to belong to this listing; the
                    // grid's other rows read as placeholders and re-load.
                    pages = LibraryPages(pageSize = PAGE_SIZE).withPage(0, items, total),
                    serverUrl = serverUrl,
                )
                ensurePages()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                if (gen != generation) return@launch
                _state.value = _state.value.copy(loading = false, error = e.message)
            }
        }
    }

    /** The A-Z index for the current listing. Any failure (404 on an older
     *  server included) just means no rail. */
    private fun fetchLetters(libraryId: String, gen: Int, watch: String?) {
        viewModelScope.launch {
            val letters: List<LibraryLetter> = try {
                repo.getLetters(libraryId, watch = watch)
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                emptyList()
            }
            if (gen != generation) return@launch
            _state.value = _state.value.copy(letters = letters)
        }
    }

    /**
     * The grid's visible rows changed (scroll, fling, rotation, a jump).
     * Loads the pages holding them plus a margin either side, and cancels
     * fetches for pages the grid has left — a fling or a rail drag across
     * the whole library would otherwise fetch every page it passed.
     */
    fun onVisibleRows(first: Int, last: Int) {
        if (last < first) return
        val rows = first..last
        if (rows != visibleRows) pageRetries.clear()
        visibleRows = rows
        ensurePages()
    }

    /**
     * A-Z rail: the screen scrolls the grid to [letter]'s row; load the page
     * that holds it now rather than after the grid's next layout. Nothing
     * before it is fetched.
     */
    fun jumpTo(letter: LibraryLetter) {
        val total = _state.value.total
        if (total == 0) return
        val first = letter.offset.coerceIn(0, total - 1)
        onVisibleRows(first, first + (visibleRows.last - visibleRows.first))
    }

    private fun ensurePages() {
        val s = _state.value
        if (s.loading || s.total == 0) return
        val wanted = s.pages.pagesFor(
            visibleRows.first - PREFETCH_ROWS,
            visibleRows.last + PREFETCH_ROWS,
        )
        val stale = pageJobs.keys.filter { it !in wanted }
        stale.forEach { pageJobs.remove(it)?.cancel() }
        for (page in wanted) {
            if (!s.pages.isLoaded(page) && page !in pageJobs) fetchPage(page)
        }
    }

    private fun fetchPage(page: Int) {
        val libraryId = currentLibrary ?: return
        val gen = generation
        val watch = _state.value.watchFilter.wire
        // Started after it's registered, so a fetch that finishes without
        // suspending can't leave a dead entry behind.
        val job = viewModelScope.launch(start = CoroutineStart.LAZY) {
            var failed = false
            try {
                val (rows, total) = repo.getItems(
                    libraryId, limit = PAGE_SIZE, offset = page * PAGE_SIZE, watch = watch,
                )
                if (gen != generation) return@launch
                pageRetries.remove(page)
                _state.value = _state.value.copy(pages = _state.value.pages.withPage(page, rows, total))
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                // The row stays a placeholder; retried below, and on scroll.
                failed = true
            } finally {
                if (pageJobs[page] == coroutineContext.job) pageJobs.remove(page)
            }
            if (failed && gen == generation) {
                val tries = (pageRetries[page] ?: 0) + 1
                pageRetries[page] = tries
                if (tries <= PAGE_RETRIES) {
                    delay(PAGE_RETRY_MS * tries)
                    if (gen == generation) ensurePages()
                }
            }
        }
        pageJobs[page] = job
        job.start()
    }

    private fun cancelPageFetches() {
        pageJobs.values.forEach { it.cancel() }
        pageJobs.clear()
        pageRetries.clear()
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
                        message = if (filter == WatchFilter.ALL) {
                            UiText.res(R.string.library_surprise_nothing)
                        } else {
                            UiText.res(R.string.library_surprise_no_match, UiText.res(filter.labelRes))
                        },
                    )
                } else {
                    _state.value.copy(surprising = false, randomPick = pick)
                }
            } catch (e: Exception) {
                val msg = if (e is HttpException && e.code() == 501) {
                    UiText.res(R.string.library_surprise_unsupported)
                } else {
                    UiText.res(R.string.library_surprise_failed)
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
                    message = UiText.res(if (watched) R.string.library_marked_watched else R.string.library_marked_unwatched),
                )
            } catch (e: Exception) {
                replaceItem(item)
                _state.value = _state.value.copy(message = markErrorText(e))
            } finally {
                marking.remove(item.id)
            }
        }
    }

    private fun replaceItem(item: MediaItem) {
        _state.value = _state.value.copy(
            pages = _state.value.pages.mapItems { if (it.id == item.id) item else it },
        )
    }

    internal companion object {
        const val PAGE_SIZE = LibraryPages.DEFAULT_PAGE_SIZE

        /** Rows loaded beyond each edge of the visible ones, so a normal
         *  scroll meets loaded cards rather than placeholders. */
        const val PREFETCH_ROWS = 30

        /** A failed page is retried on its own this many times (with a
         *  growing pause) while it stays on screen; scrolling retries too. */
        const val PAGE_RETRIES = 3
        const val PAGE_RETRY_MS = 2_000L
    }
}

data class LibraryUi(
    /** Page one of a new listing (or a pull-to-refresh) is in flight. */
    val loading: Boolean = false,
    /** The listing, [LibraryPages.total] rows long; rows load as the grid
     *  reaches them. */
    val pages: LibraryPages = LibraryPages(),
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
    /** The listing's A-Z index; empty = no rail (not sorted by title, a
     *  pre-v2.7 server, or it failed to load). */
    val letters: List<LibraryLetter> = emptyList(),
    /** A Surprise me pick is in flight. */
    val surprising: Boolean = false,
    /** One-shot: the Surprise me result the screen should open. */
    val randomPick: RandomLibraryItem? = null,
    /** One-shot snackbar text. */
    val message: UiText? = null,
) {
    /** Rows in the listing (the server's count). */
    val total: Int get() = pages.total

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
    /** "Search this library": opens the search screen scoped to this
     *  library, given its name when known (for the field's hint). */
    onSearch: ((libraryName: String?) -> Unit)? = null,
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
    val scope = rememberCoroutineScope()

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

    // The grid is the whole listing (placeholders where pages haven't
    // loaded); tell the ViewModel which rows are on screen so it loads the
    // pages there — scrolling down, back up after a letter jump, or after
    // rotating.
    LaunchedEffect(gridState) {
        snapshotFlow {
            val visible = gridState.layoutInfo.visibleItemsInfo
            if (visible.isEmpty()) null else visible.first().index to visible.last().index
        }
            .filterNotNull()
            .distinctUntilChanged()
            .collect { (first, last) -> vm.onVisibleRows(first, last) }
    }

    // The rail's highlighted letter: the section the top of the grid is in.
    val currentLetter by remember {
        derivedStateOf { letterAtRow(ui.letters, gridState.firstVisibleItemIndex) }
    }

    // Surprise me landed: open the pick (photos go to the viewer, like a tap).
    LaunchedEffect(ui.randomPick) {
        val pick = ui.randomPick ?: return@LaunchedEffect
        vm.consumeRandomPick()
        if (pick.type == "photo") onOpenPhoto(pick.id) else onOpenItem(pick.id)
    }
    // Show from a composition-scoped coroutine, not inside the effect:
    // consuming the message changes this effect's key, which cancels it —
    // and a showSnackbar running in it — on the very next frame.
    val context = LocalContext.current
    LaunchedEffect(ui.message) {
        val msg = ui.message?.asString(context) ?: return@LaunchedEffect
        vm.consumeMessage()
        scope.launch { snackbar.showSnackbar(msg) }
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
                        ui.libraryName ?: stringResource(R.string.library_title_fallback),
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.back))
                    }
                },
                actions = {
                    if (onSearch != null) {
                        IconButton(onClick = { onSearch(ui.libraryName) }) {
                            Icon(
                                Icons.Default.Search,
                                contentDescription = ui.libraryName?.let { stringResource(R.string.search_library_named, it) }
                                    ?: stringResource(R.string.search_this_library),
                            )
                        }
                    }
                    if (ui.watchControls) {
                        IconButton(onClick = vm::surpriseMe, enabled = !ui.surprising) {
                            Icon(Icons.Default.Shuffle, contentDescription = stringResource(R.string.library_surprise_me))
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
                                contentDescription = stringResource(R.string.library_photo_extras),
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
                isRefreshing = ui.loading && ui.total > 0,
                onRefresh = { vm.refresh() },
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f),
            ) {
                when {
                    ui.loading && ui.total == 0 -> LoadingState()
                    ui.error != null && ui.total == 0 -> ErrorState(ui.error, onRetry = { vm.refresh() })
                    ui.total == 0 -> EmptyState(
                        if (ui.watchFilter == WatchFilter.ALL) stringResource(R.string.library_empty)
                        else stringResource(R.string.library_filter_empty, stringResource(ui.watchFilter.labelRes)),
                    )
                    else -> Box(Modifier.fillMaxSize()) {
                        // Shown when the listing has an A-Z index (sorted by
                        // title, on a v2.7+ server). The grid leaves the
                        // rail's width free at its end so no card sits
                        // under it.
                        val rail = ui.letters.isNotEmpty()
                        val pages = ui.pages
                        val placeholderAspect = if (ui.libraryType == "photo") 1f else 2f / 3f
                        LazyVerticalGrid(
                            state = gridState,
                            columns = GridCells.Adaptive(120.dp),
                            contentPadding = PaddingValues(
                                start = 12.dp,
                                top = 12.dp,
                                bottom = 12.dp,
                                end = if (rail) LetterRailWidth + 4.dp else 12.dp,
                            ),
                            horizontalArrangement = Arrangement.spacedBy(8.dp),
                            verticalArrangement = Arrangement.spacedBy(12.dp),
                            modifier = Modifier.fillMaxSize(),
                        ) {
                            // Positional keys: a row is a place in the
                            // listing, filled in when its page lands.
                            items(
                                count = pages.total,
                                contentType = { i -> if (pages[i] == null) "placeholder" else "item" },
                            ) { i ->
                                val item = pages[i]
                                if (item == null) {
                                    PlaceholderGridItem(aspect = placeholderAspect)
                                } else {
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
                            }
                        }
                        if (rail) {
                            LetterRail(
                                letters = ui.letters,
                                current = currentLetter,
                                onPick = { letter ->
                                    vm.jumpTo(letter)
                                    scope.launch {
                                        gridState.scrollToItem(letter.offset.coerceIn(0, (ui.total - 1).coerceAtLeast(0)))
                                    }
                                },
                            )
                        }
                    }
                }
            }
        }
    }

    confirmUnwatch?.let { show ->
        AlertDialog(
            onDismissRequest = { confirmUnwatch = null },
            title = { Text(stringResource(R.string.watch_mark_all_unwatched_title)) },
            text = {
                Text(stringResource(R.string.watch_mark_all_unwatched_body, show.title))
            },
            confirmButton = {
                TextButton(onClick = {
                    confirmUnwatch = null
                    vm.setWatched(show, false)
                }) { Text(stringResource(R.string.watch_mark_unwatched)) }
            },
            dismissButton = {
                TextButton(onClick = { confirmUnwatch = null }) { Text(stringResource(R.string.cancel)) }
            },
        )
    }
}

/** A grid row whose page hasn't loaded yet: the card's shape, no content. */
@Composable
private fun PlaceholderGridItem(aspect: Float) {
    Column(modifier = Modifier.fillMaxWidth().clearAndSetSemantics { }) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(aspect)
                .clip(RoundedCornerShape(6.dp))
                .background(MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.5f)),
        )
        Spacer(Modifier.height(4.dp))
        Box(
            modifier = Modifier
                .padding(horizontal = 4.dp)
                .fillMaxWidth(0.7f)
                .height(12.dp)
                .clip(RoundedCornerShape(3.dp))
                .background(MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.5f)),
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
                label = { Text(stringResource(f.labelRes)) },
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
    val badgeLabel = badge?.description?.asString().orEmpty()
    var menuOpen by remember { mutableStateOf(false) }
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .combinedClickable(
                onClick = onClick,
                onLongClickLabel = if (markActions.isNotEmpty()) stringResource(R.string.library_watched_options) else null,
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
                    label = badgeLabel,
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
                            .semantics { contentDescription = badgeLabel },
                    )
                }
            }
            DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                markActions.forEach { watched ->
                    DropdownMenuItem(
                        text = { Text(stringResource(if (watched) R.string.watch_mark_watched else R.string.watch_mark_unwatched)) },
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
