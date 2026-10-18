package tv.onscreen.mobile.ui.watchlist

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
import androidx.compose.material.icons.automirrored.filled.Sort
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.ScrollableTabRow
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
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
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil.compose.AsyncImage
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.artworkUrl
import tv.onscreen.mobile.data.model.WatchStateValue
import tv.onscreen.mobile.data.model.WatchStatus
import tv.onscreen.mobile.data.model.WatchlistItem
import tv.onscreen.mobile.data.model.WatchlistSort
import tv.onscreen.mobile.ui.components.EmptyState
import tv.onscreen.mobile.ui.components.ErrorState
import tv.onscreen.mobile.ui.components.LoadingState

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun WatchlistScreen(
    onOpenItem: (String) -> Unit,
    onBack: () -> Unit,
    vm: WatchlistViewModel = hiltViewModel(),
) {
    val ui by vm.state.collectAsStateWithLifecycle()
    // First ON_RESUME is the initial load (the VM starts in loading=true);
    // later ones — back from a detail page where the status may have
    // changed — re-pull quietly.
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { vm.onResume() }

    val gridState = rememberLazyGridState()
    val snackbar = remember { SnackbarHostState() }

    // New query (tab / type / sort) → back to the top; a refresh keeps the
    // position (same pattern as LibraryScreen).
    var appliedQuery by rememberSaveable { mutableIntStateOf(ui.queryVersion) }
    LaunchedEffect(ui.queryVersion) {
        if (ui.queryVersion != appliedQuery) {
            appliedQuery = ui.queryVersion
            gridState.scrollToItem(0)
        }
    }

    // Endless scroll: fetch the next page as the last visible cell nears the
    // end of the loaded set and the server has more.
    val shouldLoadMore by remember {
        derivedStateOf {
            val last = gridState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: -1
            ui.items.isNotEmpty() && ui.items.size < ui.total && last >= ui.items.size - 8
        }
    }
    LaunchedEffect(shouldLoadMore, ui.loadMoreFailed) { if (shouldLoadMore) vm.loadMore() }

    // Show from a composition-scoped coroutine, not inside the effect:
    // consuming the message changes this effect's key, which cancels it.
    val snackScope = rememberCoroutineScope()
    LaunchedEffect(ui.message) {
        val msg = ui.message ?: return@LaunchedEffect
        vm.consumeMessage()
        snackScope.launch { snackbar.showSnackbar(msg) }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = {
            TopAppBar(
                title = { Text("Watchlist") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
                actions = { SortMenu(selected = ui.sort, onSelect = vm::selectSort) },
            )
        },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            val selectedTab = WATCHLIST_STATUSES.indexOf(ui.status).coerceAtLeast(0)
            ScrollableTabRow(selectedTabIndex = selectedTab, edgePadding = 8.dp) {
                WATCHLIST_STATUSES.forEachIndexed { i, s ->
                    Tab(
                        selected = i == selectedTab,
                        onClick = { vm.selectStatus(s) },
                        text = { Text(tabLabel(ui.counts, s), maxLines = 1) },
                    )
                }
            }
            if (ui.showTypeFilter) {
                TypeFilterRow(
                    options = ui.typeChoices,
                    selected = ui.type,
                    onSelect = vm::selectType,
                )
            }
            PullToRefreshBox(
                // The pull indicator only for a refresh over a populated grid;
                // the first load shows the centered spinner.
                isRefreshing = ui.loading && ui.items.isNotEmpty(),
                onRefresh = vm::refresh,
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f),
            ) {
                when {
                    ui.loading && ui.items.isEmpty() -> LoadingState()
                    ui.error != null && ui.items.isEmpty() -> ErrorState(ui.error, onRetry = vm::refresh)
                    ui.items.isEmpty() -> EmptyState(
                        "Nothing here yet — use Watching status on a title's page to add it.",
                    )
                    else -> LazyVerticalGrid(
                        state = gridState,
                        columns = GridCells.Adaptive(120.dp),
                        contentPadding = PaddingValues(12.dp),
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                        verticalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        items(ui.items, key = { it.id }) { item ->
                            WatchlistCard(
                                item = item,
                                serverUrl = ui.serverUrl,
                                busy = ui.busyId == item.id,
                                onClick = { onOpenItem(item.id) },
                                onMove = { next -> vm.changeStatus(item, next) },
                                onRemove = { vm.changeStatus(item, null) },
                            )
                        }
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
}

/** Recently changed / Title, behind the app bar's sort icon. */
@Composable
private fun SortMenu(selected: WatchlistSort, onSelect: (WatchlistSort) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        IconButton(onClick = { open = true }) {
            Icon(Icons.AutoMirrored.Filled.Sort, contentDescription = "Sort: ${selected.label}")
        }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            WatchlistSort.entries.forEach { s ->
                DropdownMenuItem(
                    text = { Text(s.label) },
                    // Non-colour cue for the active sort.
                    trailingIcon = if (s == selected) {
                        { Icon(Icons.Default.Check, contentDescription = "Selected") }
                    } else {
                        null
                    },
                    onClick = {
                        open = false
                        onSelect(s)
                    },
                )
            }
        }
    }
}

/** All + one chip per type in the current tab ("Movies (3)"). */
@Composable
private fun TypeFilterRow(
    options: List<TypeOption>,
    selected: String?,
    onSelect: (String?) -> Unit,
) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        modifier = Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = 12.dp),
    ) {
        FilterChip(
            selected = selected == null,
            onClick = { onSelect(null) },
            label = { Text("All") },
        )
        options.forEach { o ->
            FilterChip(
                selected = o.type == selected,
                onClick = { onSelect(o.type) },
                label = { Text("${o.label} (${o.count})") },
            )
        }
        // A filter the tab no longer has (its last item moved away) stays
        // visible so it can be seen — and cleared via All.
        if (selected != null && options.none { it.type == selected }) {
            FilterChip(
                selected = true,
                onClick = { onSelect(null) },
                label = { Text(typeLabel(selected)) },
            )
        }
    }
}

/**
 * Poster card: tap opens the item; long-press or the ⋮ button opens
 * "Move to …" for the other statuses and "Remove from watchlist" (the same
 * long-press + ⋮ pair as the home screen's Continue Watching cards).
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun WatchlistCard(
    item: WatchlistItem,
    serverUrl: String,
    busy: Boolean,
    onClick: () -> Unit,
    onMove: (WatchStatus) -> Unit,
    onRemove: () -> Unit,
) {
    var menuOpen by remember { mutableStateOf(false) }
    val subtitle = cardSubtitle(item)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .alpha(if (busy) 0.5f else 1f)
            .combinedClickable(
                onClick = onClick,
                onLongClickLabel = "Watchlist options",
                onLongClick = { menuOpen = true },
            ),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(2f / 3f)
                .clip(RoundedCornerShape(6.dp))
                .background(MaterialTheme.colorScheme.surfaceVariant),
        ) {
            val art = item.poster_path ?: item.thumb_path
            if (!art.isNullOrEmpty() && serverUrl.isNotEmpty()) {
                AsyncImage(
                    model = artworkUrl(serverUrl, art, width = 400),
                    contentDescription = item.title,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.fillMaxSize(),
                )
            } else {
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
            if (item.watch_state == WatchStateValue.WATCHED) {
                Surface(
                    shape = CircleShape,
                    color = MaterialTheme.colorScheme.primary,
                    contentColor = MaterialTheme.colorScheme.onPrimary,
                    modifier = Modifier
                        .align(Alignment.TopStart)
                        .padding(4.dp)
                        .clearAndSetSemantics { contentDescription = "Watched" },
                ) {
                    Icon(
                        Icons.Default.Check,
                        contentDescription = null,
                        modifier = Modifier
                            .padding(3.dp)
                            .size(14.dp),
                    )
                }
            }
            // ⋮ on the poster corner: the discoverable, accessible twin of
            // long-press. The menu anchors here for both.
            Box(modifier = Modifier.align(Alignment.TopEnd)) {
                IconButton(onClick = { menuOpen = true }, enabled = !busy) {
                    Surface(
                        shape = CircleShape,
                        color = Color.Black.copy(alpha = 0.55f),
                        contentColor = Color.White,
                    ) {
                        Icon(
                            Icons.Default.MoreVert,
                            contentDescription = "More options for ${item.title}",
                            modifier = Modifier
                                .padding(4.dp)
                                .size(18.dp),
                        )
                    }
                }
                DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                    moveTargets(item).forEach { s ->
                        DropdownMenuItem(
                            text = { Text("Move to ${statusLabel(s)}") },
                            enabled = !busy,
                            onClick = {
                                menuOpen = false
                                onMove(s)
                            },
                        )
                    }
                    HorizontalDivider()
                    DropdownMenuItem(
                        text = { Text("Remove from watchlist") },
                        leadingIcon = { Icon(Icons.Default.Close, contentDescription = null) },
                        enabled = !busy,
                        onClick = {
                            menuOpen = false
                            onRemove()
                        },
                    )
                }
            }
        }
        Spacer(Modifier.height(4.dp))
        Text(
            item.title,
            style = MaterialTheme.typography.bodySmall,
            maxLines = if (subtitle.isNotEmpty()) 1 else 2,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(horizontal = 4.dp),
        )
        if (subtitle.isNotEmpty()) {
            Text(
                subtitle,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(horizontal = 4.dp),
            )
        }
    }
}
