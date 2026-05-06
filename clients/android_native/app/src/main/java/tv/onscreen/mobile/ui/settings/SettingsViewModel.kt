package tv.onscreen.mobile.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.prefs.PlaybackPrefs
import tv.onscreen.mobile.data.prefs.ServerPrefs
import javax.inject.Inject

@HiltViewModel
class SettingsViewModel @Inject constructor(
    private val prefs: PlaybackPrefs,
    private val server: ServerPrefs,
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
     *  AppNav reroutes to /pair the moment isLoggedIn flips false. */
    fun signOut() {
        viewModelScope.launch { server.clearAuth() }
    }

    /** Forget the server entirely (URL + tokens + user). Used when
     *  the operator wants to re-pair against a different deployment.
     *  Same nav effect as signOut — the absence of a server URL
     *  routes back to /pair, which then asks for the URL again. */
    fun disconnectServer() {
        viewModelScope.launch { server.clearAll() }
    }
}
