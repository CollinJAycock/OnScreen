package tv.onscreen.mobile.ui.discover

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.model.DiscoverItem
import tv.onscreen.mobile.data.repository.DiscoverRepository
import javax.inject.Inject

/**
 * Discover screen state. Three flavours of error shape it cares about:
 *   - empty query (just don't fetch)
 *   - TMDB key not configured server-side (show a hint, not a stack trace)
 *   - generic network / 5xx (red banner)
 *
 * The "request" path keeps a per-tmdb-id submitting set so the UI can
 * disable just that one card's button while the POST is in flight,
 * without a global spinner.
 */
data class DiscoverUi(
    val query: String = "",
    val results: List<DiscoverItem> = emptyList(),
    val loading: Boolean = false,
    val error: String? = null,
    /** TMDB IDs currently submitting a request — drives per-card
     *  button disable. */
    val submitting: Set<Int> = emptySet(),
    /** TMDB IDs with a successful submit during this session — flips
     *  the button label to "Requested" so the user gets local feedback
     *  without re-fetching the discover list. */
    val submitted: Set<Int> = emptySet(),
)

@HiltViewModel
class DiscoverViewModel @Inject constructor(
    private val repo: DiscoverRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(DiscoverUi())
    val state: StateFlow<DiscoverUi> = _state.asStateFlow()

    /** Update the query string. Search is fired by the user — we
     *  don't auto-fire on every keystroke since each call hits TMDB
     *  and bills against the daily cap. */
    fun onQueryChanged(q: String) {
        _state.value = _state.value.copy(query = q)
    }

    /** Run a search. Trimmed-empty queries no-op. */
    fun search() {
        val q = _state.value.query.trim()
        if (q.isEmpty()) return
        _state.value = _state.value.copy(loading = true, error = null)
        viewModelScope.launch {
            try {
                val results = repo.search(q)
                _state.value = _state.value.copy(loading = false, results = results)
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false, error = e.message ?: "Search failed")
            }
        }
    }

    /**
     * Submit a request for [item]. Optimistic on the local `submitted`
     * set — flips immediately so the UI can show "Requested." On
     * failure we revert and surface the error in [DiscoverUi.error]
     * so the user knows the request didn't reach the server.
     */
    fun request(item: DiscoverItem) {
        if (item.tmdb_id in _state.value.submitting) return
        if (item.tmdb_id in _state.value.submitted) return
        if (item.has_active_request) return
        // Defence-in-depth: the UI also greys the button on in_library
        // items, but the VM no-ops too so a programmatic call (e.g. a
        // future "request all visible" action) doesn't fan out a
        // duplicate POST per existing title.
        if (item.in_library) return
        _state.value = _state.value.copy(
            submitting = _state.value.submitting + item.tmdb_id,
        )
        viewModelScope.launch {
            try {
                repo.createRequest(item.type, item.tmdb_id)
                _state.value = _state.value.copy(
                    submitting = _state.value.submitting - item.tmdb_id,
                    submitted = _state.value.submitted + item.tmdb_id,
                )
            } catch (e: Exception) {
                _state.value = _state.value.copy(
                    submitting = _state.value.submitting - item.tmdb_id,
                    error = e.message ?: "Request failed",
                )
            }
        }
    }
}
