package tv.onscreen.mobile.ui.hub

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.model.HubData
import tv.onscreen.mobile.data.model.HubItem
import tv.onscreen.mobile.data.model.HubRowPref
import tv.onscreen.mobile.data.model.Library
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.HubRepository
import tv.onscreen.mobile.data.repository.LibraryRepository
import tv.onscreen.mobile.data.repository.PreferencesRepository
import javax.inject.Inject

@HiltViewModel
class HubViewModel @Inject constructor(
    private val hubRepo: HubRepository,
    private val libraryRepo: LibraryRepository,
    private val prefs: ServerPrefs,
    private val prefsRepo: PreferencesRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(HubUi())
    val state: StateFlow<HubUi> = _state.asStateFlow()

    /** Continue Watching ids with a dismiss in flight — a second tap on
     *  the same tile is dropped. */
    private val dismissing = mutableSetOf<String>()

    init {
        load()
    }

    fun load() = fetch(showSpinner = true)

    /** Re-pull the hub without the pull-to-refresh spinner. Called when
     *  the home screen comes back into view, so Continue Watching / Next Up
     *  reflect what was just watched or marked on a detail page. Skipped
     *  before the first load lands and while a load is running. */
    fun refreshQuietly() {
        val s = _state.value
        if (s.hub == null || s.loading) return
        fetch(showSpinner = false)
    }

    private fun fetch(showSpinner: Boolean) {
        viewModelScope.launch {
            if (showSpinner) _state.value = _state.value.copy(loading = true, error = null)
            try {
                val hub = hubRepo.getHub()
                val libs = libraryRepo.getLibraries()
                val serverUrl = prefs.getServerUrl().orEmpty()
                // Best-effort: a prefs failure must not blank the home screen —
                // fall back to the default (empty) layout.
                val layout = try {
                    prefsRepo.get().hub_layout ?: emptyList()
                } catch (_: Exception) {
                    emptyList()
                }
                _state.value = _state.value.copy(
                    loading = false,
                    hub = hub,
                    libraries = libs,
                    serverUrl = serverUrl,
                    hubLayout = layout,
                    error = null,
                )
            } catch (e: Exception) {
                // A quiet refresh keeps the rows on screen; only a visible
                // load surfaces the error.
                _state.value = if (showSpinner) {
                    _state.value.copy(loading = false, error = e.message)
                } else {
                    _state.value.copy(loading = false)
                }
            }
        }
    }

    /**
     * Remove [item] from Continue Watching. Optimistic: the tile leaves
     * every Continue Watching list at once; if the server call fails it goes
     * back where it was (unless a refresh already brought it back) and a
     * message is posted.
     */
    fun dismissContinueWatching(item: HubItem) {
        val hub = _state.value.hub ?: return
        if (!dismissing.add(item.id)) return
        val removal = ContinueWatchingRemoval.remove(hub, item.id)
        _state.value = _state.value.copy(hub = removal.hub)
        viewModelScope.launch {
            try {
                hubRepo.dismissContinueWatching(item.id)
            } catch (_: Exception) {
                val current = _state.value.hub
                _state.value = _state.value.copy(
                    hub = current?.let { removal.restoreInto(it) },
                    message = "Couldn't remove from Continue Watching",
                )
            } finally {
                dismissing.remove(item.id)
            }
        }
    }

    /** The screen showed [HubUi.message]; clear it. */
    fun consumeMessage() {
        _state.value = _state.value.copy(message = null)
    }
}

data class HubUi(
    val loading: Boolean = true,
    val hub: HubData? = null,
    val libraries: List<Library> = emptyList(),
    val serverUrl: String = "",
    // User's saved hub row order + visibility (from web). Empty = default layout.
    val hubLayout: List<HubRowPref> = emptyList(),
    val error: String? = null,
    /** One-shot snackbar text (e.g. a failed Continue Watching removal). */
    val message: String? = null,
)

/**
 * An item taken out of every Continue Watching list of a [HubData], with
 * enough recorded to put it back at the same positions. Pure so the
 * remove / restore bookkeeping is unit-testable without a ViewModel.
 */
internal class ContinueWatchingRemoval private constructor(
    val hub: HubData,
    private val combined: Removed?,
    private val tv: Removed?,
    private val movies: Removed?,
    private val other: Removed?,
) {
    private class Removed(val item: HubItem, val index: Int)

    /** Put the item back into [into] where it was — list by list, and only
     *  where it isn't already present (a refresh may have re-added it). */
    fun restoreInto(into: HubData): HubData = into.copy(
        continue_watching = restore(into.continue_watching, combined),
        continue_watching_tv = into.continue_watching_tv?.let { restore(it, tv) },
        continue_watching_movies = into.continue_watching_movies?.let { restore(it, movies) },
        continue_watching_other = into.continue_watching_other?.let { restore(it, other) },
    )

    companion object {
        fun remove(hub: HubData, id: String): ContinueWatchingRemoval {
            val (cw, cwR) = removeFrom(hub.continue_watching, id)
            val tvR = hub.continue_watching_tv?.let { removeFrom(it, id) }
            val movR = hub.continue_watching_movies?.let { removeFrom(it, id) }
            val othR = hub.continue_watching_other?.let { removeFrom(it, id) }
            return ContinueWatchingRemoval(
                hub = hub.copy(
                    continue_watching = cw,
                    continue_watching_tv = tvR?.first,
                    continue_watching_movies = movR?.first,
                    continue_watching_other = othR?.first,
                ),
                combined = cwR,
                tv = tvR?.second,
                movies = movR?.second,
                other = othR?.second,
            )
        }

        private fun removeFrom(list: List<HubItem>, id: String): Pair<List<HubItem>, Removed?> {
            val idx = list.indexOfFirst { it.id == id }
            if (idx < 0) return list to null
            return (list.take(idx) + list.drop(idx + 1)) to Removed(list[idx], idx)
        }

        private fun restore(list: List<HubItem>, removed: Removed?): List<HubItem> {
            if (removed == null || list.any { it.id == removed.item.id }) return list
            val at = removed.index.coerceIn(0, list.size)
            return list.take(at) + removed.item + list.drop(at)
        }
    }
}
