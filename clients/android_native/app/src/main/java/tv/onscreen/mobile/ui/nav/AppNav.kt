package tv.onscreen.mobile.ui.nav

import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
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
import tv.onscreen.mobile.ui.player.PlayerScreen
import tv.onscreen.mobile.ui.search.SearchScreen
import tv.onscreen.mobile.ui.series.SeriesScreen
import tv.onscreen.mobile.ui.settings.AboutScreen
import tv.onscreen.mobile.ui.settings.SettingsScreen
import javax.inject.Inject

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
    val signedIn by vm.signedIn.collectAsState()
    // Offline-mode routing: signed-in users with no network at cold
    // start land on Downloads instead of Hub. Hub fetches /hub which
    // would just error out, and the user's offline content is by
    // definition the manifest of completed downloads.
    val start = when (signedIn) {
        null -> return       // splash flicker avoidance — wait for first emission
        true -> if (vm.coldStartOnline) Routes.HUB else Routes.DOWNLOADS
        false -> Routes.PAIR
    }

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
                onOpenItem = { id -> nav.navigate(Routes.item(id)) },
                onOpenLibrary = { id -> nav.navigate(Routes.library(id)) },
                onOpenSearch = { nav.navigate(Routes.SEARCH) },
                onOpenFavorites = { nav.navigate(Routes.FAVORITES) },
                onOpenHistory = { nav.navigate(Routes.HISTORY) },
                onOpenCollections = { nav.navigate(Routes.COLLECTIONS) },
                onOpenDownloads = { nav.navigate(Routes.DOWNLOADS) },
                onOpenPlaylists = { nav.navigate(Routes.PLAYLISTS) },
                onOpenSettings = { nav.navigate(Routes.SETTINGS) },
            )
        }
        composable(Routes.SETTINGS) {
            SettingsScreen(
                onBack = { nav.popBackStack() },
                onOpenAbout = { nav.navigate(Routes.ABOUT) },
            )
        }
        composable(Routes.ABOUT) {
            AboutScreen(onBack = { nav.popBackStack() })
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
                onOpenItem = { id -> nav.navigate(Routes.photo(id)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(Routes.DOWNLOADS) {
            DownloadsScreen(
                onOpenItem = { id -> nav.navigate(Routes.item(id)) },
                onPlay = { id -> nav.navigate(Routes.player(id)) },
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
                onOpenItem = { id -> nav.navigate(Routes.item(id)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(Routes.HISTORY) {
            HistoryScreen(
                onOpenItem = { id -> nav.navigate(Routes.item(id)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(Routes.COLLECTIONS) {
            CollectionsScreen(
                onOpenCollection = { id -> nav.navigate(Routes.collection(id)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(
            Routes.COLLECTION,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            CollectionDetailScreen(
                collectionId = entry.arguments!!.getString("id")!!,
                onOpenItem = { id -> nav.navigate(Routes.item(id)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(
            Routes.LIBRARY,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            LibraryScreen(
                libraryId = entry.arguments!!.getString("id")!!,
                onOpenItem = { id -> nav.navigate(Routes.item(id)) },
                onOpenPhoto = { id -> nav.navigate(Routes.photo(id)) },
                onOpenPhotoExtras = { libId -> nav.navigate(Routes.photoExtras(libId)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(
            Routes.ITEM,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            ItemDetailScreen(
                itemId = entry.arguments!!.getString("id")!!,
                onPlay = { id -> nav.navigate(Routes.player(id)) },
                onOpenItem = { id -> nav.navigate(Routes.item(id)) },
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
                onOpenItem = { id -> nav.navigate(Routes.item(id)) },
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
                onOpenSeries = { id -> nav.navigate(Routes.series(id)) },
                onOpenBook = { id -> nav.navigate(Routes.item(id)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable(
            Routes.SERIES,
            arguments = listOf(navArgument("id") { type = NavType.StringType }),
        ) { entry ->
            SeriesScreen(
                seriesId = entry.arguments!!.getString("id")!!,
                onOpenBook = { id -> nav.navigate(Routes.item(id)) },
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
