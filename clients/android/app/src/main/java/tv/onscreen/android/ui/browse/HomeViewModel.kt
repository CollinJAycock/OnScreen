package tv.onscreen.android.ui.browse

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
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
    // v2.5 per-user watch rows (empty on older servers → rows hidden).
    val nextUp: List<HubItem> = emptyList(),
    val planToWatch: List<HubItem> = emptyList(),
    // v2.6 promoted collections (empty on older servers).
    val collectionRows: List<HubCollectionRow> = emptyList(),
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
            nextUp.isNotEmpty() ||
            planToWatch.isNotEmpty() ||
            collectionRows.any { it.items.isNotEmpty() } ||
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

    /** One-shot UI events (toasts). Kept out of [HomeUiState] on purpose: the
     *  fragment rebuilds every row whenever the state changes, and a message
     *  riding in the state would force a rebuild just to show a toast. */
    private val _events = MutableSharedFlow<HomeEvent>(extraBufferCapacity = 4)
    val events: SharedFlow<HomeEvent> = _events

    /** Continue Watching ids a hub refresh must not bring back yet, mapped to
     *  the [loadSeq] current when the server committed the dismissal
     *  ([DISMISS_IN_FLIGHT] until then). A refresh (onResume reloads on every
     *  return) whose read may predate the commit — one in flight during the
     *  POST, or started before it — would re-add the tile, so it is filtered.
     *  Only until a load that STARTED after the commit completes: from then on
     *  the server stops returning the item on its own, and it SHOULD come back
     *  later if the user watches it again. */
    private val pendingDismiss = mutableMapOf<String, Long>()

    /** Incremented as each [load] starts. */
    private var loadSeq = 0L

    // NOTE: no init{load()} — HomeFragment.onResume() drives the load. Having both
    // double-fetched every endpoint on launch and rebuilt the rows twice (flicker +
    // focus reset). onResume always fires after onViewCreated, so the first launch
    // still loads exactly once.

    fun load() {
        val seq = ++loadSeq
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

                // Dismissals this read may predate: still in flight, or
                // committed after this load started.
                val stale = pendingDismiss.filterValues { it >= seq }.keys
                fun List<HubItem>.withoutPending() =
                    if (stale.isEmpty()) this else filterNot { it.id in stale }
                // This read began after the rest committed — the server's
                // answer is authoritative for them from here on.
                pendingDismiss.entries.removeAll { it.value < seq }

                _uiState.value = HomeUiState(
                    isLoading = false,
                    continueWatchingTV = tv.withoutPending(),
                    continueWatchingMovies = movies.withoutPending(),
                    continueWatchingOther = other.withoutPending(),
                    recentlyAdded = hub.recently_added,
                    trending = hub.trending,
                    nextUp = hub.next_up,
                    planToWatch = hub.plan_to_watch,
                    collectionRows = hub.collection_rows,
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

    /**
     * Remove a Continue Watching tile: gone from every CW row at once
     * (optimistic), then POST /items/{id}/dismiss-continue-watching. On
     * failure the tile goes back where it was — unless a refresh already
     * brought it back — and a [HomeEvent.DismissFailed] is emitted.
     */
    fun dismissContinueWatching(item: HubItem) {
        if (item.id in pendingDismiss) return
        pendingDismiss[item.id] = DISMISS_IN_FLIGHT
        val before = _uiState.value
        val tv = removeById(before.continueWatchingTV, item.id)
        val movies = removeById(before.continueWatchingMovies, item.id)
        val other = removeById(before.continueWatchingOther, item.id)
        _uiState.value = before.copy(
            // Rendered now even while an onResume refresh is in flight — the
            // fragment skips isLoading states, so copying that flag hid the
            // removal until the refresh landed.
            isLoading = false,
            continueWatchingTV = tv.first,
            continueWatchingMovies = movies.first,
            continueWatchingOther = other.first,
        )
        viewModelScope.launch {
            try {
                hubRepo.dismissContinueWatching(item.id)
                // Keep filtering until a load that starts after this commit
                // completes (see pendingDismiss).
                pendingDismiss[item.id] = loadSeq
                _events.tryEmit(HomeEvent.Dismissed(item.id))
            } catch (e: Exception) {
                pendingDismiss.remove(item.id)
                val cur = _uiState.value
                _uiState.value = cur.copy(
                    continueWatchingTV = restoreAt(cur.continueWatchingTV, tv.second),
                    continueWatchingMovies = restoreAt(cur.continueWatchingMovies, movies.second),
                    continueWatchingOther = restoreAt(cur.continueWatchingOther, other.second),
                )
                _events.tryEmit(HomeEvent.DismissFailed(rateLimited = (e as? retrofit2.HttpException)?.code() == 429))
            }
        }
    }

    companion object {
        /** [pendingDismiss] marker for a dismissal the server hasn't answered. */
        private const val DISMISS_IN_FLIGHT = Long.MAX_VALUE

        /** Remove by id: the new list plus the removed item and its old index
         *  (null when absent). */
        internal fun removeById(list: List<HubItem>, id: String): Pair<List<HubItem>, IndexedValue<HubItem>?> {
            val index = list.indexOfFirst { it.id == id }
            if (index < 0) return list to null
            return (list.subList(0, index) + list.subList(index + 1, list.size)) to IndexedValue(index, list[index])
        }

        /** Put a removed item back at its old position, unless something (a
         *  refresh) re-added it meanwhile. */
        internal fun restoreAt(list: List<HubItem>, removed: IndexedValue<HubItem>?): List<HubItem> {
            removed ?: return list
            if (list.any { it.id == removed.value.id }) return list
            val at = removed.index.coerceIn(0, list.size)
            return list.subList(0, at) + removed.value + list.subList(at, list.size)
        }
    }
}

sealed interface HomeEvent {
    /** A Continue Watching tile was removed server-side. */
    data class Dismissed(val itemId: String) : HomeEvent
    /** Removing a Continue Watching tile failed; the tile was restored. */
    data class DismissFailed(val rateLimited: Boolean) : HomeEvent
}
