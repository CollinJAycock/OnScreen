package tv.onscreen.android.ui.browse

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.*
import tv.onscreen.android.data.repository.CollectionRepository
import tv.onscreen.android.data.repository.HubRepository
import tv.onscreen.android.data.repository.LibraryRepository
import tv.onscreen.android.data.repository.NotificationsRepository
import javax.inject.Inject

data class HomeUiState(
    val isLoading: Boolean = true,
    val continueWatching: List<HubItem> = emptyList(),
    val recentlyAdded: List<HubItem> = emptyList(),
    val trending: List<HubItem> = emptyList(),
    val becauseYouWatched: List<BecauseYouWatched> = emptyList(),
    val libraryPreviews: List<Pair<Library, List<MediaItem>>> = emptyList(),
    val collections: List<MediaCollection> = emptyList(),
    val unreadNotifications: Int = 0,
    val error: String? = null,
)

@HiltViewModel
class HomeViewModel @Inject constructor(
    private val hubRepo: HubRepository,
    private val libraryRepo: LibraryRepository,
    private val collectionRepo: CollectionRepository,
    private val notificationsRepo: NotificationsRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(HomeUiState())
    val uiState: StateFlow<HomeUiState> = _uiState

    init {
        load()
        startNotificationsStream()
    }

    private fun startNotificationsStream() {
        viewModelScope.launch {
            while (true) {
                try {
                    notificationsRepo.subscribe().collect { _ ->
                        _uiState.value = _uiState.value.copy(
                            unreadNotifications = _uiState.value.unreadNotifications + 1,
                        )
                    }
                } catch (_: Exception) {
                    // Reconnect after short backoff.
                }
                delay(5_000)
            }
        }
    }

    fun load() {
        viewModelScope.launch {
            _uiState.value = HomeUiState(isLoading = true)
            try {
                val hubDeferred = async { hubRepo.getHub() }
                val libsDeferred = async { libraryRepo.getLibraries() }
                val colsDeferred = async { collectionRepo.getCollections() }
                val unreadDeferred = async { notificationsRepo.unreadCount() }

                val hub = hubDeferred.await()
                val libs = libsDeferred.await()
                val cols = colsDeferred.await()
                val unread = unreadDeferred.await()

                // Load first 20 items from each library in parallel.
                val previews = libs.map { lib ->
                    async {
                        try {
                            val (items, _) = libraryRepo.getItems(lib.id, limit = 20)
                            lib to items
                        } catch (_: Exception) {
                            lib to emptyList()
                        }
                    }
                }.awaitAll()

                _uiState.value = HomeUiState(
                    isLoading = false,
                    continueWatching = hub.continue_watching,
                    recentlyAdded = hub.recently_added,
                    trending = hub.trending,
                    becauseYouWatched = hub.because_you_watched,
                    libraryPreviews = previews,
                    collections = cols,
                    unreadNotifications = unread.toInt().coerceAtMost(Int.MAX_VALUE),
                )
            } catch (e: Exception) {
                _uiState.value = HomeUiState(isLoading = false, error = e.message)
            }
        }
    }
}
