package tv.onscreen.mobile.ui.settings

import android.content.Context
import android.content.Intent
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.downloads.OnScreenDownloadManager
import tv.onscreen.mobile.data.prefs.PlaybackPrefs
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.AuthRepository
import tv.onscreen.mobile.playback.PlaybackService
import tv.onscreen.mobile.playback.StreamTokenVault
import javax.inject.Inject

@HiltViewModel
class SettingsViewModel @Inject constructor(
    private val prefs: PlaybackPrefs,
    private val server: ServerPrefs,
    private val auth: AuthRepository,
    private val downloads: OnScreenDownloadManager,
    @ApplicationContext private val appContext: Context,
) : ViewModel() {

    val downloadOnWifiOnly: Flow<Boolean> = prefs.downloadOnWifiOnly
    val warnOnCellularStream: Flow<Boolean> = prefs.warnOnCellularStream

    /** Currently bound server URL (for the About row + the
     *  disconnect-confirm message). Empty when none — that should
     *  never happen on this screen since auth gating routes back to
     *  /pair, but kept defensive. */
    val serverUrl: Flow<String?> = server.serverUrl
    val username: Flow<String?> = server.username

    fun setDownloadOnWifiOnly(value: Boolean) {
        viewModelScope.launch { prefs.setDownloadOnWifiOnly(value) }
    }

    fun setWarnOnCellularStream(value: Boolean) {
        viewModelScope.launch { prefs.setWarnOnCellularStream(value) }
    }

    /** Sign out without forgetting the server URL — next launch
     *  starts at the pair screen with the server already filled in.
     *  AppNav reroutes to /pair the moment isLoggedIn flips false.
     *
     *  Goes through AuthRepository.logout so the refresh token is REVOKED
     *  server-side and the per-identity caches are dropped. Clearing local
     *  prefs alone (what this used to do) left the session valid on the
     *  server — a stolen refresh token kept working after the user signed
     *  out — and left the next user on this phone inheriting the previous
     *  one's cached preferences. */
    fun signOut() {
        // Stop background audio FIRST and synchronously — not inside the
        // detached andThen. Sign-out is the user's stop-everything control,
        // and PlaybackService is a process-lifetime MediaSessionService that
        // nothing else tears down: without this, music kept coming out of the
        // speaker with the signed-out account's title and artwork on the lock
        // screen. Doing it ahead of the network call means an unreachable
        // server cannot leave playback running.
        stopBackgroundAudio()
        // Downloads are erased too: the manifest is a process-wide singleton
        // with no owner field, so anything left behind is listed — and
        // playable offline — for whoever signs in next.
        auth.logoutDetached(andThen = { downloads.cancelAllAndClear() })
    }

    /** Forget the server entirely (URL + tokens + user). Used when
     *  the operator wants to re-pair against a different deployment.
     *  Same nav effect as signOut — the absence of a server URL
     *  routes back to /pair, which then asks for the URL again. */
    fun disconnectServer() {
        stopBackgroundAudio()
        // Revoke + drop caches while the server URL still resolves, THEN
        // forget the URL — order matters, the logout call needs it. Media
        // downloaded from this server goes with it.
        auth.logoutDetached(andThen = {
            downloads.cancelAllAndClear()
            server.clearAll()
        })
    }

    /** Tear down the background-audio service and drop any cached playback
     *  credentials. Mirrors the TV client's SettingsViewModel.stopBackgroundAudio.
     *
     *  stopService rather than a controller command: PlayerScreen deliberately
     *  releases only its MediaController on back-out ("backing out keeps audio
     *  playing"), so the service outlives every UI surface and nothing else in
     *  the app stops it. */
    private fun stopBackgroundAudio() {
        runCatching {
            appContext.stopService(Intent(appContext, PlaybackService::class.java))
        }
        // The vault holds stream/asset tokens keyed by URL. They are
        // short-lived and in-memory only, but there is no reason for the next
        // account on this device to inherit them.
        StreamTokenVault.clear()
    }
}
