package tv.onscreen.android.ui.settings

import android.content.Context
import dagger.hilt.android.qualifiers.ApplicationContext
import androidx.media3.common.util.UnstableApi
import tv.onscreen.android.playback.AudioHandoff
import tv.onscreen.android.playback.StreamTokenVault
import tv.onscreen.android.playback.WatchNextManager
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import retrofit2.HttpException
import tv.onscreen.android.data.api.apiError
import tv.onscreen.android.data.model.QualityProfileBody
import tv.onscreen.android.data.model.UserPreferences
import tv.onscreen.android.R
import tv.onscreen.android.data.repository.AuthRepository
import tv.onscreen.android.data.repository.LanguageRepository
import tv.onscreen.android.data.repository.PreferencesRepository
import tv.onscreen.android.data.repository.ScrobbleRepository
import tv.onscreen.android.i18n.AppLanguage
import tv.onscreen.android.i18n.UiText
import javax.inject.Inject

data class SettingsUiState(
    val preferences: UserPreferences = UserPreferences(),
    val username: String? = null,
    val serverUrl: String? = null,
    val saved: Boolean = false,
    val error: UiText? = null,
    val loading: Boolean = true,
    /** True only after a real preferences GET succeeded. The form stays
     *  read-only until then — editing the all-null defaults fed fabricated
     *  values into a full-object PUT that reverted other clients' changes. */
    val prefsLoaded: Boolean = false,
    // Per-user ListenBrainz scrobble link. The token is write-only, so we only
    // track whether one is linked and whether export is on.
    val scrobbleLinked: Boolean = false,
    val scrobbleEnabled: Boolean = false,
    /** This device's interface language ("en", "es", "zh-Hans"); null = it
     *  follows the device's language. */
    val appLanguage: String? = null,
    /** The interface language picked here couldn't be saved to the account
     *  (a server before v3): it is used on this device only, and the
     *  Language row says so. */
    val languageSaveFailed: Boolean = false,
)

@HiltViewModel
class SettingsViewModel @Inject constructor(
    private val prefsRepo: PreferencesRepository,
    private val authRepo: AuthRepository,
    private val scrobbleRepo: ScrobbleRepository,
    private val watchNext: WatchNextManager,
    @ApplicationContext private val appContext: Context,
    private val language: LanguageRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow(SettingsUiState())
    val uiState: StateFlow<SettingsUiState> = _uiState

    fun load(username: String?, serverUrl: String?) {
        _uiState.value = _uiState.value.copy(
            username = username, serverUrl = serverUrl, loading = true, appLanguage = language.choice(),
        )
        viewModelScope.launch {
            try {
                // Bypass the process-lifetime cache: Settings is where the
                // user LOOKS at their preferences, so it must show what the
                // server has now — the cached snapshot could predate changes
                // made on the web, and editing it PUT those stale values back.
                val prefs = prefsRepo.refresh()
                _uiState.value = _uiState.value.copy(
                    preferences = prefs, loading = false, prefsLoaded = true,
                    // The read may have just applied the account's language.
                    appLanguage = language.choice(),
                )
            } catch (e: Exception) {
                _uiState.value = _uiState.value.copy(error = e.message?.let(UiText::Raw), loading = false)
            }
        }
        loadScrobble()
    }

    /**
     * Settings › Language: [tag] ("en", "es", "zh-Hans"; null = the device's
     * language) applies at once — the screen is recreated in it — and is
     * saved to the account. When the save fails the language stays on this
     * device and [SettingsUiState.languageSaveFailed] says so. Runs in the
     * ViewModel, which outlives that recreate.
     */
    fun chooseLanguage(tag: String?) {
        _uiState.value = _uiState.value.copy(appLanguage = AppLanguage.supported(tag))
        viewModelScope.launch {
            val saved = language.choose(tag)
            _uiState.value = _uiState.value.copy(languageSaveFailed = !saved)
        }
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
                _uiState.value = _uiState.value.copy(error = errorText(e, R.string.error_save_failed))
            }
        }
    }

    /**
     * Change one field of the playback-quality profile (v2.6). The profile is
     * re-read just before the write and [change] applied to that, so the
     * other fields — forced_subtitles_only included — go back as the server
     * has them now. A refused value (400) shows the server's reason.
     */
    fun saveQuality(change: (QualityProfileBody) -> QualityProfileBody) {
        // Refuse to write until a real load succeeded — see prefsLoaded.
        if (!_uiState.value.prefsLoaded) return
        viewModelScope.launch {
            try {
                val saved = prefsRepo.setQualityProfile(change)
                _uiState.value = _uiState.value.copy(preferences = saved, saved = true, error = null)
                scheduleSavedClear()
            } catch (e: HttpException) {
                val reason = if (e.code() == 400) e.apiError()?.message?.takeIf { it.isNotBlank() } else null
                _uiState.value = _uiState.value.copy(
                    error = reason?.let(UiText::Raw)
                        ?: if (e.code() == 400) {
                            UiText.Res(R.string.quality_not_allowed)
                        } else {
                            UiText.Res(R.string.error_save_failed_http, e.code())
                        },
                )
            } catch (e: Exception) {
                _uiState.value = _uiState.value.copy(error = errorText(e, R.string.error_save_failed))
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
                _uiState.value = _uiState.value.copy(error = errorText(e, R.string.scrobble_link_failed))
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
                _uiState.value = _uiState.value.copy(error = errorText(e, R.string.scrobble_unlink_failed))
            }
        }
    }

    /** The exception's own message (the server's, in the app's language),
     *  else [fallback]. */
    private fun errorText(e: Exception, fallback: Int): UiText =
        e.message?.let(UiText::Raw) ?: UiText.Res(fallback)

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
        // Shared with the involuntary sign-out path (SignOutTeardown) so both
        // tear down identically.
        AudioHandoff.stopAll(appContext)
        // Drop in-memory stream credentials the player's resolver would
        // otherwise keep re-attaching. authRepo.logout() clears it too; this
        // covers the window before that returns.
        StreamTokenVault.clear()
    }
}
