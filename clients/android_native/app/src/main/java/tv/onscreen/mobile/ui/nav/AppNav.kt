package tv.onscreen.mobile.ui.nav

import androidx.activity.compose.BackHandler
import androidx.annotation.StringRes
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Checklist
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationRail
import androidx.compose.material3.NavigationRailItem
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.compose.currentBackStackEntryAsState
import tv.onscreen.mobile.R
import tv.onscreen.mobile.ui.adaptive.ListDetail
import tv.onscreen.mobile.ui.adaptive.ProvideContentWidth
import tv.onscreen.mobile.ui.adaptive.rememberWindowLayout
import tv.onscreen.mobile.ui.components.MiniPlayerBar
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.getValue
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.navigation.NavController
import androidx.navigation.NavOptionsBuilder
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import tv.onscreen.mobile.data.network.ConnectivityObserver
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.ui.collections.CollectionDetailScreen
import tv.onscreen.mobile.ui.collections.CollectionsScreen
import tv.onscreen.mobile.ui.downloads.DownloadsScreen
import tv.onscreen.mobile.ui.favorites.FavoritesScreen
import tv.onscreen.mobile.ui.history.HistoryScreen
import tv.onscreen.mobile.ui.hub.HubScreen
import tv.onscreen.mobile.ui.item.ItemDetailScreen
import tv.onscreen.mobile.ui.library.LibraryScreen
import tv.onscreen.mobile.ui.author.AuthorScreen
import tv.onscreen.mobile.ui.pair.PairScreen
import tv.onscreen.mobile.ui.photo.PhotoExtrasScreen
import tv.onscreen.mobile.ui.photo.PhotoViewerScreen
import tv.onscreen.mobile.ui.playlists.PlaylistsScreen
import tv.onscreen.mobile.ui.book.BookReaderScreen
import tv.onscreen.mobile.ui.player.PlayerScreen
import tv.onscreen.mobile.ui.search.SearchScreen
import tv.onscreen.mobile.ui.series.SeriesScreen
import tv.onscreen.mobile.ui.settings.AboutScreen
import tv.onscreen.mobile.ui.settings.PlaybackQualityScreen
import tv.onscreen.mobile.ui.settings.ScrobbleScreen
import tv.onscreen.mobile.ui.settings.SecurityScreen
import tv.onscreen.mobile.ui.settings.SettingsScreen
import tv.onscreen.mobile.ui.watchlist.WatchlistScreen
import javax.inject.Inject

/**
 * Debounced navigation. A fast double-tap on a list row can fire two
 * navigate() calls before the destination's back-stack entry reaches
 * RESUMED, pushing the same destination twice. Only navigate while the
 * current entry is RESUMED so the second tap of a double-tap is dropped.
 */
private fun NavController.navigateDebounced(
    route: String,
    builder: NavOptionsBuilder.() -> Unit = {},
) {
    if (currentBackStackEntry?.lifecycle?.currentState == Lifecycle.State.RESUMED) {
        navigate(route, builder)
    }
}

/**
 * Debounced back, navigateDebounced's counterpart. A screen's back arrow
 * popped whatever was on top: a double tap (or a tap while the previous
 * pop was still animating) popped the screen underneath too, and on the
 * first screen the last entry, leaving the NavHost nothing to show: a black
 * screen with the app still running (seen on a Meta Quest, where pointing
 * makes double taps easy). Only pop while the current entry is RESUMED, and
 * never the first screen.
 */
private fun NavController.popBackDebounced() {
    if (currentBackStackEntry?.lifecycle?.currentState == Lifecycle.State.RESUMED &&
        previousBackStackEntry != null
    ) {
        popBackStack()
    }
}

@HiltViewModel
class RootViewModel @Inject constructor(
    prefs: ServerPrefs,
    connectivity: ConnectivityObserver,
) : ViewModel() {
    val signedIn: StateFlow<Boolean?> =
        prefs.isLoggedIn
            .map<Boolean, Boolean?> { it }
            .stateIn(viewModelScope, SharingStarted.Eagerly, null)

    /** Cold-start online state. Captured once at construction so the
     *  start destination doesn't flap if the network comes up while
     *  the user is on the splash. Live state for screens to react to
     *  comes from [ConnectivityObserver.isOnline] directly. */
    val coldStartOnline: Boolean = connectivity.isOnline.value
}

@Composable
fun AppNav(vm: RootViewModel = hiltViewModel()) {
    val nav = rememberNavController()
    val signedIn by vm.signedIn.collectAsStateWithLifecycle()
    // Offline-mode routing: signed-in users with no network at cold
    // start land on Downloads instead of Hub. Hub fetches /hub which
    // would just error out, and the user's offline content is by
    // definition the manifest of completed downloads.
    val start = when (signedIn) {
        null -> return       // splash flicker avoidance — wait for first emission
        true -> if (vm.coldStartOnline) Routes.HUB else Routes.DOWNLOADS
        false -> Routes.PAIR
    }

    // Mini-player bar: visible on shell destinations while background audio
    // is active; hidden on immersive routes (player, reader, photo viewer)
    // and pre-auth. Column layout (not an overlay) so screens shrink rather
    // than get covered when the bar appears.
    val backStackEntry by nav.currentBackStackEntryAsState()
    val currentRoute = backStackEntry?.destination?.route
    val immersive = currentRoute == Routes.PLAYER || currentRoute == Routes.PAIR ||
        currentRoute == Routes.PHOTO || currentRoute == Routes.BOOK

    // Tablets and Chromebooks (a window at least 600dp wide and not short):
    // a navigation rail to the top-level sections, which a phone reaches
    // through Home's overflow menu. The rail marks the last section visited,
    // so a page opened from Home (a title, a library) keeps Home selected.
    val showRail = rememberWindowLayout().navigationRail && !immersive
    var section by rememberSaveable { mutableStateOf(start) }
    LaunchedEffect(currentRoute) {
        if (currentRoute != null && railDestinations.any { it.route == currentRoute }) section = currentRoute
    }
    Row(modifier = Modifier.fillMaxSize()) {
        if (showRail) {
            AppNavigationRail(selected = section, onSelect = { route -> nav.navigateTopLevel(route) })
        }
        Column(modifier = Modifier.weight(1f)) {
            ProvideContentWidth(modifier = Modifier.weight(1f)) {
                AppNavHost(nav, start)
            }
            if (!immersive) {
                MiniPlayerBar(onOpen = { id -> nav.navigateDebounced(Routes.player(id)) })
            }
        }
    }
}

private data class RailDestination(val route: String, val icon: ImageVector, @StringRes val label: Int)

/** The rail's sections: Home and the overflow menu's most-used entries. */
private val railDestinations = listOf(
    RailDestination(Routes.HUB, Icons.Default.Home, R.string.nav_home),
    RailDestination(Routes.SEARCH, Icons.Default.Search, R.string.search),
    RailDestination(Routes.WATCHLIST, Icons.Default.Checklist, R.string.nav_watchlist),
    RailDestination(Routes.FAVORITES, Icons.Default.Favorite, R.string.nav_favorites),
    RailDestination(Routes.DOWNLOADS, Icons.Default.Download, R.string.nav_downloads),
    RailDestination(Routes.SETTINGS, Icons.Default.Settings, R.string.settings),
)

@Composable
private fun AppNavigationRail(selected: String, onSelect: (String) -> Unit) {
    NavigationRail {
        railDestinations.forEach { d ->
            NavigationRailItem(
                selected = selected == d.route,
                onClick = { onSelect(d.route) },
                icon = { Icon(d.icon, contentDescription = null) },
                label = { Text(stringResource(d.label)) },
            )
        }
    }
}

/**
 * A rail section, the usual top-level way: back to the start destination
 * with each section's back stack saved and restored, and no duplicate when
 * the section is already showing. Debounced like [navigateDebounced].
 */
private fun NavController.navigateTopLevel(route: String) {
    if (currentBackStackEntry?.lifecycle?.currentState != Lifecycle.State.RESUMED) return
    navigate(route) {
        popUpTo(graph.findStartDestination().id) { saveState = true }
        launchSingleTop = true
        restoreState = true
    }
}

/**
 * The title page, as its own route or as the detail pane of a library
 * ([inPane]). Play, other titles and books open full screen either way.
 */
@Composable
private fun ItemDetailRoute(nav: NavController, itemId: String, onBack: () -> Unit, inPane: Boolean = false) {
    // Redirect destinations for photo / book_author / book_series items pop
    // the current item route as they push, so Back returns to the source list
    // (library, hub row, favorites, etc.) — not to this detail screen which
    // would just redirect again. A pane has no item route to pop: it closes.
    val redirect: (String) -> Unit = { route ->
        if (inPane) {
            onBack()
            nav.navigateDebounced(route)
        } else {
            nav.navigate(route) { popUpTo(Routes.ITEM) { inclusive = true } }
        }
    }
    ItemDetailScreen(
        itemId = itemId,
        onPlay = { id -> nav.navigateDebounced(Routes.player(id)) },
        onPlayFromStart = { id -> nav.navigateDebounced(Routes.player(id, fromStart = true)) },
        onPlayAt = { id, ms -> nav.navigateDebounced(Routes.player(id, startMs = ms)) },
        onPlayVersion = { id, fileId -> nav.navigateDebounced(Routes.player(id, fileId = fileId)) },
        onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
        onOpenBook = { id -> nav.navigateDebounced(Routes.book(id)) },
        onOpenPhoto = { id -> redirect(Routes.photo(id)) },
        onOpenAuthor = { id -> redirect(Routes.author(id)) },
        onOpenSeries = { id -> redirect(Routes.series(id)) },
        onBack = onBack,
    )
}

@Composable
private fun AppNavHost(nav: androidx.navigation.NavHostController, start: String) {
    NavHost(navController = nav, startDestination = start) {
        composable(Routes.PAIR) {
            PairScreen(onPaired = {
                nav.navigate(Routes.HUB) {
                    popUpTo(Routes.PAIR) { inclusive = true }
                }
            })
        }
        composable(Routes.HUB) {
            HubScreen(
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onOpenLibrary = { id -> nav.navigateDebounced(Routes.library(id)) },
                onOpenSearch = { nav.navigateDebounced(Routes.SEARCH) },
                onOpenFavorites = { nav.navigateDebounced(Routes.FAVORITES) },
                onOpenWatchlist = { nav.navigateDebounced(Routes.WATCHLIST) },
                onOpenHistory = { nav.navigateDebounced(Routes.HISTORY) },
                onOpenCollections = { nav.navigateDebounced(Routes.COLLECTIONS) },
                onOpenDownloads = { nav.navigateDebounced(Routes.DOWNLOADS) },
                onOpenPlaylists = { nav.navigateDebounced(Routes.PLAYLISTS) },
                onOpenSettings = { nav.navigateDebounced(Routes.SETTINGS) },
            )
        }
        composable(Routes.SETTINGS) {
            // Wide window: a sub-screen opens beside the list instead of over
            // it. Narrow: its own route, as on a phone. A sub-screen open in
            // the pane moves to its route if the window narrows.
            val twoPane = rememberWindowLayout().twoPane
            var pane by rememberSaveable { mutableStateOf<String?>(null) }
            LaunchedEffect(twoPane) {
                if (!twoPane) pane?.let { route -> pane = null; nav.navigateDebounced(route) }
            }
            BackHandler(enabled = twoPane && pane != null) { pane = null }
            // WHY: route these through navigateDebounced like every
            // other tap target. Raw navigate() let a fast double-tap on
            // a settings row fire twice before the entry reached RESUMED,
            // pushing the same sub-screen onto the stack twice.
            val open: (String) -> Unit = { route -> if (twoPane) pane = route else nav.navigateDebounced(route) }
            ListDetail(
                showDetail = twoPane && pane != null,
                listWeight = 0.42f,
                list = {
                    SettingsScreen(
                        onBack = { nav.popBackDebounced() },
                        onOpenAbout = { open(Routes.ABOUT) },
                        onOpenSecurity = { open(Routes.SECURITY) },
                        onOpenScrobble = { open(Routes.SCROBBLE) },
                        onOpenPlaybackQuality = { open(Routes.PLAYBACK_QUALITY) },
                    )
                },
                detail = {
                    val close = { pane = null }
                    when (pane) {
                        Routes.ABOUT -> AboutScreen(onBack = close)
                        Routes.SECURITY -> SecurityScreen(onBack = close)
                        Routes.SCROBBLE -> ScrobbleScreen(onBack = close)
                        Routes.PLAYBACK_QUALITY -> PlaybackQualityScreen(onBack = close)
                    }
                },
            )
        }
        composable(Routes.PLAYBACK_QUALITY) {
            PlaybackQualityScreen(onBack = { nav.popBackDebounced() })
        }
        composable(Routes.ABOUT) {
            AboutScreen(onBack = { nav.popBackDebounced() })
        }
        composable(Routes.SECURITY) {
            SecurityScreen(onBack = { nav.popBackDebounced() })
        }
        composable(Routes.SCROBBLE) {
            ScrobbleScreen(onBack = { nav.popBackDebounced() })
        }
        composable(Routes.PLAYLISTS) {
            PlaylistsScreen(onBack = { nav.popBackDebounced() })
        }
        composable(
            Routes.PHOTO_EXTRAS,
            arguments = listOf(navArgument("libraryId") { type = NavType.StringType }),
        ) { entry ->
            PhotoExtrasScreen(
                libraryId = entry.arguments!!.getString("libraryId")!!,
                onOpenItem = { id -> nav.navigateDebounced(Routes.photo(id)) },
                onBack = { nav.popBackDebounced() },
            )
        }
        composable(Routes.DOWNLOADS) {
            DownloadsScreen(
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onPlay = { id -> nav.navigateDebounced(Routes.player(id)) },
                // WHY: "Go online" is only correct when Downloads is the
                // offline cold-start ROOT (nothing beneath it on the back
                // stack) — then onGoOnline's popUpTo(DOWNLOADS)+navigate(HUB)
                // cleanly swaps the root for Hub. When Downloads was opened
                // over Hub via overflow there IS an entry beneath it, so this
                // is false and the screen hides the button; without the gate
                // that path stacked a duplicate Hub (and Back already returns
                // to Hub there anyway).
                isOfflineRoot = nav.previousBackStackEntry == null,
                onGoOnline = {
                    // From offline-mode start: replace Downloads on
                    // the back stack with Hub so Back exits the app
                    // (matches the normal cold-start back behaviour
                    // from Hub).
                    nav.navigate(Routes.HUB) {
                        popUpTo(Routes.DOWNLOADS) { inclusive = true }
                    }
                },
                onBack = { nav.popBackDebounced() },
            )
        }
        composable(Routes.FAVORITES) {
            FavoritesScreen(
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onBack = { nav.popBackDebounced() },
            )
        }
        composable(Routes.WATCHLIST) {
            WatchlistScreen(
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onBack = { nav.popBackDebounced() },
            )
        }
        composable(Routes.HISTORY) {
            HistoryScreen(
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onBack = { nav.popBackDebounced() },
            )
        }
        composable(Routes.COLLECTIONS) {
            CollectionsScreen(
                onOpenCollection = { id -> nav.navigateDebounced(Routes.collection(id)) },
                onBack = { nav.popBackDebounced() },
            )
        }
        composable(
            Routes.COLLECTION,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            CollectionDetailScreen(
                collectionId = entry.arguments!!.getString("id")!!,
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onBack = { nav.popBackDebounced() },
            )
        }
        composable(
            Routes.LIBRARY,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            // Wide window: the title picked in the grid opens beside it
            // (list-detail); the grid keeps the whole width until then.
            // Narrow: the title's own route. An open title moves to its
            // route if the window narrows.
            val libraryId = entry.arguments!!.getString("id")!!
            val twoPane = rememberWindowLayout().twoPane
            var selected by rememberSaveable { mutableStateOf<String?>(null) }
            LaunchedEffect(twoPane) {
                if (!twoPane) selected?.let { id -> selected = null; nav.navigateDebounced(Routes.item(id)) }
            }
            BackHandler(enabled = selected != null) { selected = null }
            ListDetail(
                showDetail = twoPane && selected != null,
                listWeight = 0.55f,
                list = {
                    LibraryScreen(
                        libraryId = libraryId,
                        onOpenItem = { id -> if (twoPane) selected = id else nav.navigateDebounced(Routes.item(id)) },
                        onOpenPhoto = { id, position, watch -> nav.navigateDebounced(Routes.photo(id, position, watch)) },
                        onOpenPhotoExtras = { libId -> nav.navigateDebounced(Routes.photoExtras(libId)) },
                        onSearch = { name -> nav.navigateDebounced(Routes.librarySearch(libraryId, name)) },
                        onBack = { nav.popBackDebounced() },
                    )
                },
                detail = {
                    selected?.let { id ->
                        ItemDetailRoute(nav, itemId = id, onBack = { selected = null }, inPane = true)
                    }
                },
            )
        }
        composable(
            Routes.ITEM,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            ItemDetailRoute(nav, itemId = entry.arguments!!.getString("id")!!, onBack = { nav.popBackDebounced() })
        }
        composable(Routes.SEARCH) {
            SearchScreen(
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onBack = { nav.popBackDebounced() },
            )
        }
        // The same screen scoped to one library; SearchViewModel reads the
        // arguments from its SavedStateHandle. Back returns to the library
        // grid where it was.
        composable(
            Routes.LIBRARY_SEARCH,
            arguments = listOf(
                navArgument("libraryId") { type = NavType.StringType },
                navArgument("libraryName") {
                    type = NavType.StringType
                    nullable = true
                    defaultValue = null
                },
            ),
        ) {
            SearchScreen(
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onBack = { nav.popBackDebounced() },
            )
        }
        composable(
            Routes.PHOTO,
            arguments = listOf(
                navArgument("id") { type = NavType.StringType },
                navArgument("pos") {
                    type = NavType.IntType
                    defaultValue = -1
                },
                navArgument("watch") {
                    type = NavType.StringType
                    nullable = true
                    defaultValue = null
                },
            ),
        ) { entry ->
            PhotoViewerScreen(
                itemId = entry.arguments!!.getString("id")!!,
                position = entry.arguments!!.getInt("pos", -1).takeIf { it >= 0 },
                watch = entry.arguments!!.getString("watch"),
                onBack = { nav.popBackDebounced() },
            )
        }
        composable(
            Routes.AUTHOR,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            AuthorScreen(
                authorId = entry.arguments!!.getString("id")!!,
                onOpenSeries = { id -> nav.navigateDebounced(Routes.series(id)) },
                onOpenBook = { id -> nav.navigateDebounced(Routes.item(id)) },
                onBack = { nav.popBackDebounced() },
            )
        }
        composable(
            Routes.SERIES,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            SeriesScreen(
                seriesId = entry.arguments!!.getString("id")!!,
                onOpenBook = { id -> nav.navigateDebounced(Routes.item(id)) },
                onBack = { nav.popBackDebounced() },
            )
        }
        composable(
            Routes.BOOK,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            BookReaderScreen(
                itemId = entry.arguments!!.getString("id")!!,
                onBack = { nav.popBackDebounced() },
            )
        }
        composable(
            Routes.PLAYER,
            arguments = listOf(
                navArgument("id") { type = NavType.StringType },
                navArgument("fromStart") {
                    type = NavType.BoolType
                    defaultValue = false
                },
                navArgument("startMs") {
                    type = NavType.LongType
                    defaultValue = -1L
                },
                navArgument("fileId") {
                    type = NavType.StringType
                    nullable = true
                    defaultValue = null
                },
            ),
        ) { entry ->
            PlayerScreen(
                itemId = entry.arguments!!.getString("id")!!,
                fromStart = entry.arguments!!.getBoolean("fromStart"),
                startMs = entry.arguments!!.getLong("startMs").takeIf { it >= 0 },
                fileId = entry.arguments!!.getString("fileId"),
                onClose = { nav.popBackDebounced() },
                onNext = { nextId ->
                    // Replace current player route with the next
                    // sibling so back returns to the detail page,
                    // not to a chain of player screens stacked up.
                    nav.navigate(Routes.player(nextId)) {
                        popUpTo(Routes.PLAYER) { inclusive = true }
                    }
                },
            )
        }
    }
}
