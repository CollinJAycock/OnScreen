package tv.onscreen.mobile.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.repository.ScrobbleRepository
import javax.inject.Inject

/** UI state for the per-user ListenBrainz link screen. The token is write-only,
 *  so once linked we only know linked/enabled — never the token itself. */
sealed interface ScrobbleUiState {
    data object Loading : ScrobbleUiState
    data class Unlinked(val error: String? = null) : ScrobbleUiState
    data class Linked(val enabled: Boolean, val error: String? = null) : ScrobbleUiState
}

@HiltViewModel
class ScrobbleViewModel @Inject constructor(
    private val repo: ScrobbleRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<ScrobbleUiState>(ScrobbleUiState.Loading)
    val state: StateFlow<ScrobbleUiState> = _state.asStateFlow()

    // True while a link/unlink request is in flight so the screen can disable
    // its buttons and show progress.
    private val _busy = MutableStateFlow(false)
    val busy: StateFlow<Boolean> = _busy.asStateFlow()

    init { loadStatus() }

    fun loadStatus() {
        viewModelScope.launch {
            _state.value = try {
                val s = repo.status()
                if (s.listenbrainz_linked) ScrobbleUiState.Linked(s.listenbrainz_enabled)
                else ScrobbleUiState.Unlinked()
            } catch (e: Exception) {
                ScrobbleUiState.Unlinked(e.message)
            }
        }
    }

    /** Link (or replace) the token. Linking implies enabling — there's nothing
     *  to pause until a token exists, and the server forces enabled=false on an
     *  empty token. */
    fun link(token: String) {
        val t = token.trim()
        if (t.isEmpty()) return
        viewModelScope.launch {
            _busy.value = true
            try {
                repo.setListenBrainz(t, enabled = true)
                loadStatus()
            } catch (e: Exception) {
                // A failed *replace* must not look unlinked — the prior token is
                // still live server-side. Keep the current state, attach the error.
                val msg = e.message ?: "Could not link account"
                _state.value = when (val cur = _state.value) {
                    is ScrobbleUiState.Linked -> cur.copy(error = msg)
                    else -> ScrobbleUiState.Unlinked(msg)
                }
            } finally {
                _busy.value = false
            }
        }
    }

    /** Unlink — empty token clears the link server-side. */
    fun unlink() {
        viewModelScope.launch {
            _busy.value = true
            try {
                repo.setListenBrainz("", enabled = false)
                loadStatus()
            } catch (e: Exception) {
                val cur = _state.value as? ScrobbleUiState.Linked
                _state.value = cur?.copy(error = e.message ?: "Could not unlink account")
                    ?: ScrobbleUiState.Unlinked(e.message)
            } finally {
                _busy.value = false
            }
        }
    }
}
