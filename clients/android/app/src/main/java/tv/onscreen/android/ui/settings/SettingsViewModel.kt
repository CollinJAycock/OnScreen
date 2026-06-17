package tv.onscreen.android.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import tv.onscreen.android.data.model.UserPreferences
import tv.onscreen.android.data.repository.AuthRepository
import tv.onscreen.android.data.repository.PreferencesRepository
import tv.onscreen.android.data.repository.ScrobbleRepository
import javax.inject.Inject

data class SettingsUiState(
    val preferences: UserPreferences = UserPreferences(),
    val username: String? = null,
    val serverUrl: String? = null,
    val saved: Boolean = false,
    val error: String? = null,
    val loading: Boolean = true,
    // Per-user ListenBrainz scrobble link. The token is write-only, so we only
    // track whether one is linked and whether export is on.
    val scrobbleLinked: Boolean = false,
    val scrobbleEnabled: Boolean = false,
)

@HiltViewModel
class SettingsViewModel @Inject constructor(
    private val prefsRepo: PreferencesRepository,
    private val authRepo: AuthRepository,
    private val scrobbleRepo: ScrobbleRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(SettingsUiState())
    val uiState: StateFlow<SettingsUiState> = _uiState

    fun load(username: String?, serverUrl: String?) {
        _uiState.value = _uiState.value.copy(username = username, serverUrl = serverUrl, loading = true)
        viewModelScope.launch {
            try {
                val prefs = prefsRepo.get()
                _uiState.value = _uiState.value.copy(preferences = prefs, loading = false)
            } catch (e: Exception) {
                _uiState.value = _uiState.value.copy(error = e.message, loading = false)
            }
        }
        loadScrobble()
    }

    private fun loadScrobble() {
        viewModelScope.launch {
            try {
                val s = scrobbleRepo.status()
                _uiState.value = _uiState.value.copy(
                    scrobbleLinked = s.listenbrainz_linked,
                    scrobbleEnabled = s.listenbrainz_enabled,
                )
            } catch (_: Exception) {
                // Best-effort; leave the linked/enabled defaults.
            }
        }
    }

    fun savePreferences(prefs: UserPreferences) {
        viewModelScope.launch {
            try {
                val saved = prefsRepo.set(prefs)
                _uiState.value = _uiState.value.copy(preferences = saved, saved = true, error = null)
                scheduleSavedClear()
            } catch (e: Exception) {
                _uiState.value = _uiState.value.copy(error = e.message ?: "save failed")
            }
        }
    }

    /** Link (or replace) the ListenBrainz token. Linking implies enabling. */
    fun linkListenBrainz(token: String) {
        val t = token.trim()
        if (t.isEmpty()) return
        viewModelScope.launch {
            try {
                scrobbleRepo.setListenBrainz(t, enabled = true)
                _uiState.value = _uiState.value.copy(saved = true, error = null)
                scheduleSavedClear()
                loadScrobble()
            } catch (e: Exception) {
                _uiState.value = _uiState.value.copy(error = e.message ?: "Could not link account")
            }
        }
    }

    /** Unlink — an empty token clears the link server-side. */
    fun unlinkListenBrainz() {
        viewModelScope.launch {
            try {
                scrobbleRepo.setListenBrainz("", enabled = false)
                _uiState.value = _uiState.value.copy(saved = true, error = null)
                scheduleSavedClear()
                loadScrobble()
            } catch (e: Exception) {
                _uiState.value = _uiState.value.copy(error = e.message ?: "Could not unlink account")
            }
        }
    }

    private var savedClearJob: Job? = null

    /** Keep the "Saved" confirmation visible briefly, then clear it. The old code
     *  cleared the flag in the same render pass that set it (the fragment called
     *  clearSavedFlag() on observing saved=true), so the status flipped back to
     *  GONE in one frame and the user never saw it. */
    private fun scheduleSavedClear() {
        savedClearJob?.cancel()
        savedClearJob = viewModelScope.launch {
            delay(SAVED_VISIBLE_MS)
            if (_uiState.value.saved) _uiState.value = _uiState.value.copy(saved = false)
        }
    }

    fun clearSavedFlag() {
        savedClearJob?.cancel()
        if (_uiState.value.saved) _uiState.value = _uiState.value.copy(saved = false)
    }

    companion object {
        private const val SAVED_VISIBLE_MS = 2000L
    }

    suspend fun logout() {
        authRepo.logout()
    }
}
