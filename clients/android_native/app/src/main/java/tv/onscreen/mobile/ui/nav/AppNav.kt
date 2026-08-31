package tv.onscreen.mobile.ui.nav

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.navigation.compose.currentBackStackEntryAsState
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
import tv.onscreen.mobile.ui.settings.ScrobbleScreen
import tv.onscreen.mobile.ui.settings.SecurityScreen
import tv.onscreen.mobile.ui.settings.SettingsScreen
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
    Column(modifier = Modifier.fillMaxSize()) {
        Box(modifier = Modifier.weight(1f)) {
            AppNavHost(nav, start)
        }
        if (!immersive) {
            MiniPlayerBar(onOpen = { id -> nav.navigateDebounced(Routes.player(id)) })
        }
    }
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
                onOpenHistory = { nav.navigateDebounced(Routes.HISTORY) },
                onOpenCollections = { nav.navigateDebounced(Routes.COLLECTIONS) },
                onOpenDownloads = { nav.navigateDebounced(Routes.DOWNLOADS) },
                onOpenPlaylists = { nav.navigateDebounced(Routes.PLAYLISTS) },
                onOpenSettings = { nav.navigateDebounced(Routes.SETTINGS) },
            )
        }
        composable(Routes.SETTINGS) {
            SettingsScreen(
                onBack = { nav.popBackStack() },
                // WHY: route these through navigateDebounced like every
                // other tap target. Raw navigate() let a fast double-tap on
                // a settings row fire twice before the entry reached RESUMED,
                // pushing the same sub-screen onto the stack twice.
                onOpenAbout = { nav.navigateDebounced(Routes.ABOUT) },
                onOpenSecurity = { nav.navigateDebounced(Routes.SECURITY) },
                onOpenScrobble = { nav.navigateDebounced(Routes.SCROBBLE) },
            )
        }
        composable(Routes.ABOUT) {
            AboutScreen(onBack = { nav.popBackStack() })
        }
        composable(Routes.SECURITY) {
            SecurityScreen(onBack = { nav.popBackStack() })
        }
        composable(Routes.SCROBBLE) {
            ScrobbleScreen(onBack = { nav.popBackStack() })
        }
        composable(Routes.PLAYLISTS) {
            PlaylistsScreen(onBack = { nav.popBackStack() })
        }
        composable(
            Routes.PHOTO_EXTRAS,
            arguments = listOf(navArgument("libraryId") { type = NavType.StringType }),
        ) { entry ->
            PhotoExtrasScreen(
                libraryId = entry.arguments!!.getString("libraryId")!!,
                onOpenItem = { id -> nav.navigateDebounced(Routes.photo(id)) },
                onBack = { nav.popBackStack() },
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
                onBack = { nav.popBackStack() },
            )
        }
        composable(Routes.FAVORITES) {
            FavoritesScreen(
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(Routes.HISTORY) {
            HistoryScreen(
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(Routes.COLLECTIONS) {
            CollectionsScreen(
                onOpenCollection = { id -> nav.navigateDebounced(Routes.collection(id)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(
            Routes.COLLECTION,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            CollectionDetailScreen(
                collectionId = entry.arguments!!.getString("id")!!,
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(
            Routes.LIBRARY,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            LibraryScreen(
                libraryId = entry.arguments!!.getString("id")!!,
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onOpenPhoto = { id -> nav.navigateDebounced(Routes.photo(id)) },
                onOpenPhotoExtras = { libId -> nav.navigateDebounced(Routes.photoExtras(libId)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(
            Routes.ITEM,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            ItemDetailScreen(
                itemId = entry.arguments!!.getString("id")!!,
                onPlay = { id -> nav.navigateDebounced(Routes.player(id)) },
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onOpenBook = { id -> nav.navigateDebounced(Routes.book(id)) },
                // Redirect destinations for photo / book_author /
                // book_series items pop the current item route as they
                // push, so Back returns to the source list (library,
                // hub row, favorites, etc.) — not to this detail
                // screen which would just redirect again.
                onOpenPhoto = { id ->
                    nav.navigate(Routes.photo(id)) {
                        popUpTo(Routes.ITEM) { inclusive = true }
                    }
                },
                onOpenAuthor = { id ->
                    nav.navigate(Routes.author(id)) {
                        popUpTo(Routes.ITEM) { inclusive = true }
                    }
                },
                onOpenSeries = { id ->
                    nav.navigate(Routes.series(id)) {
                        popUpTo(Routes.ITEM) { inclusive = true }
                    }
                },
                onBack = { nav.popBackStack() },
            )
        }
        composable(Routes.SEARCH) {
            SearchScreen(
                onOpenItem = { id -> nav.navigateDebounced(Routes.item(id)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(
            Routes.PHOTO,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            PhotoViewerScreen(
                itemId = entry.arguments!!.getString("id")!!,
                onBack = { nav.popBackStack() },
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
                onBack = { nav.popBackStack() },
            )
        }
        composable(
            Routes.SERIES,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            SeriesScreen(
                seriesId = entry.arguments!!.getString("id")!!,
                onOpenBook = { id -> nav.navigateDebounced(Routes.item(id)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(
            Routes.BOOK,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            BookReaderScreen(
                itemId = entry.arguments!!.getString("id")!!,
                onBack = { nav.popBackStack() },
            )
        }
        composable(
            Routes.PLAYER,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            PlayerScreen(
                itemId = entry.arguments!!.getString("id")!!,
                onClose = { nav.popBackStack() },
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
