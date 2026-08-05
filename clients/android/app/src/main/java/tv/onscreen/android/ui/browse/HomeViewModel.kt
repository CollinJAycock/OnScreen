package tv.onscreen.android.ui.browse

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.supervisorScope
import tv.onscreen.android.data.model.*
import tv.onscreen.android.data.repository.CollectionRepository
import tv.onscreen.android.data.repository.HubRepository
import tv.onscreen.android.data.repository.LibraryRepository
import tv.onscreen.android.data.repository.PreferencesRepository
import javax.inject.Inject

data class HomeUiState(
    val isLoading: Boolean = true,
    val continueWatchingTV: List<HubItem> = emptyList(),
    val continueWatchingMovies: List<HubItem> = emptyList(),
    val continueWatchingOther: List<HubItem> = emptyList(),
    val recentlyAdded: List<HubItem> = emptyList(),
    val trending: List<HubItem> = emptyList(),
    val libraryPreviews: List<Pair<Library, List<MediaItem>>> = emptyList(),
    val collections: List<MediaCollection> = emptyList(),
    // User's saved hub row order + visibility (from web). Empty = default layout.
    val hubLayout: List<HubRowPref> = emptyList(),
    val error: String? = null,
) {
    /** Any row populated — i.e. there is something on screen worth keeping
     *  when a later refresh fails. Mirrors the fragment's own check. */
    val hasContent: Boolean
        get() = continueWatchingTV.isNotEmpty() ||
            continueWatchingMovies.isNotEmpty() ||
            continueWatchingOther.isNotEmpty() ||
            recentlyAdded.isNotEmpty() ||
            trending.isNotEmpty() ||
            libraryPreviews.isNotEmpty() ||
            collections.isNotEmpty()
}

@HiltViewModel
class HomeViewModel @Inject constructor(
    private val hubRepo: HubRepository,
    private val libraryRepo: LibraryRepository,
    private val collectionRepo: CollectionRepository,
    private val prefsRepo: PreferencesRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(HomeUiState())
    val uiState: StateFlow<HomeUiState> = _uiState

    // NOTE: no init{load()} — HomeFragment.onResume() drives the load. Having both
    // double-fetched every endpoint on launch and rebuilt the rows twice (flicker +
    // focus reset). onResume always fires after onViewCreated, so the first launch
    // still loads exactly once.

    fun load() {
        viewModelScope.launch {
            // Mark loading WITHOUT discarding the current rows: onResume
            // refreshes on every return to Home, and blanking here made the
            // populated hub flash empty on each visit (and left nothing to
            // fall back to if the refresh then failed).
            _uiState.value = _uiState.value.copy(isLoading = true, error = null)
            try {
                // supervisorScope so a child failure throws into the
                // try/catch below instead of cancelling its siblings
                // and propagating up the viewModelScope (the default
                // structured-concurrency behaviour). Without this, a
                // hub-fetch failure tears down libs + cols asyncs
                // before the catch block reaches the user.
                data class Loaded(
                    val hub: HubData,
                    val libs: List<Library>,
                    val cols: List<MediaCollection>,
                    val layout: List<HubRowPref>,
                )
                val (hub, libs, cols, layout) = supervisorScope {
                    val hubDeferred = async { hubRepo.getHub() }
                    val libsDeferred = async { libraryRepo.getLibraries() }
                    val colsDeferred = async { collectionRepo.getCollections() }
                    // Best-effort: a prefs failure must not blank the home screen —
                    // fall back to the default (empty) layout.
                    val layoutDeferred = async {
                        try {
                            prefsRepo.get().hub_layout ?: emptyList()
                        } catch (_: Exception) {
                            emptyList()
                        }
                    }
                    Loaded(hubDeferred.await(), libsDeferred.await(), colsDeferred.await(), layoutDeferred.await())
                }

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

                // Server populates the three split arrays on builds
                // that ship the per-show dedupe; older servers only
                // return the combined continue_watching feed, in
                // which case we filter client-side.
                val tv = hub.continue_watching_tv
                    ?: hub.continue_watching.filter { it.type == "episode" }
                val movies = hub.continue_watching_movies
                    ?: hub.continue_watching.filter { it.type == "movie" }
                val other = hub.continue_watching_other
                    ?: hub.continue_watching.filter { it.type != "episode" && it.type != "movie" }

                _uiState.value = HomeUiState(
                    isLoading = false,
                    continueWatchingTV = tv,
                    continueWatchingMovies = movies,
                    continueWatchingOther = other,
                    recentlyAdded = hub.recently_added,
                    trending = hub.trending,
                    libraryPreviews = previews,
                    collections = cols,
                    hubLayout = layout,
                )
            } catch (e: Exception) {
                // Keep whatever is already on screen. onResume re-loads on
                // every return to Home, so a single blip used to replace a
                // fully-populated hub with a full-screen error overlay —
                // whose "Change server" button (one D-pad press from the
                // focused Retry, and unconfirmed until now) wipes the install.
                // Surface the error only when there's nothing to fall back to.
                val prev = _uiState.value
                _uiState.value = if (prev.hasContent) {
                    prev.copy(isLoading = false, error = null)
                } else {
                    HomeUiState(isLoading = false, error = e.message)
                }
            }
        }
    }
}
