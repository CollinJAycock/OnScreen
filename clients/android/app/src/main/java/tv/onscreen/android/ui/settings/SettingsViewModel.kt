package tv.onscreen.android.ui.settings

import android.content.Context
import android.content.Intent
import dagger.hilt.android.qualifiers.ApplicationContext
import androidx.media3.common.util.UnstableApi
import tv.onscreen.android.playback.AudioHandoff
import tv.onscreen.android.playback.OnScreenMediaSessionService
import tv.onscreen.android.playback.WatchNextManager
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
    /** True only after a real preferences GET succeeded. The form stays
     *  read-only until then — editing the all-null defaults fed fabricated
     *  values into a full-object PUT that reverted other clients' changes. */
    val prefsLoaded: Boolean = false,
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
    private val watchNext: WatchNextManager,
    @ApplicationContext private val appContext: Context,
) : ViewModel() {

    private val _uiState = MutableStateFlow(SettingsUiState())
    val uiState: StateFlow<SettingsUiState> = _uiState

    fun load(username: String?, serverUrl: String?) {
        _uiState.value = _uiState.value.copy(username = username, serverUrl = serverUrl, loading = true)
        viewModelScope.launch {
            try {
                // Bypass the process-lifetime cache: Settings is where the
                // user LOOKS at their preferences, so it must show what the
                // server has now — the cached snapshot could predate changes
                // made on the web, and editing it PUT those stale values back.
                val prefs = prefsRepo.refresh()
                _uiState.value = _uiState.value.copy(preferences = prefs, loading = false, prefsLoaded = true)
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
        // Refuse to write until a real load succeeded — see prefsLoaded.
        if (!_uiState.value.prefsLoaded) return
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

    /** Sign out and scrub the device-level traces of the session.
     *
     *  Clearing tokens is not enough on a TV. Two surfaces outlive the app's
     *  own state and are both visible to whoever picks up the remote next:
     *
     *   - The launcher's Continue Watching strip. Those rows live in the SYSTEM
     *     TV provider, carry the titles and posters the previous user was
     *     watching, and deep-link straight back into them.
     *   - A parked audio player. Backgrounded music keeps playing after sign-out
     *     — streaming on the now-signed-out user's stream token — because the
     *     MediaSession service holds the player independently of the UI.
     */
    suspend fun logout() {
        // Provider query + one delete per row = cross-process binder IPC —
        // off the main thread, or sign-out visibly janks on a loaded strip.
        kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
            watchNext.removeAll()
        }
        stopBackgroundAudio()
        authRepo.logout()
    }

    // OnScreenMediaSessionService is annotated @UnstableApi (it extends
    // media3's MediaSessionService), so naming its class here requires opting
    // in. androidx.annotation.OptIn rather than kotlin.OptIn: the marker is a
    // Java annotation, and that is the form lint's UnsafeOptInUsageError
    // recognises — same as PlaybackFragment. Scoped to this one function.
    @androidx.annotation.OptIn(UnstableApi::class)
    private fun stopBackgroundAudio() {
        // Release the parked player first so the service's own teardown has
        // nothing left to hand back, then stop the service.
        AudioHandoff.peek()?.let { player ->
            runCatching {
                player.stop()
                player.release()
            }
        }
        AudioHandoff.clear()
        runCatching {
            appContext.stopService(
                Intent(appContext, OnScreenMediaSessionService::class.java)
            )
        }
    }
}
