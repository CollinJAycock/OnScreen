package tv.onscreen.mobile.data.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map

private val Context.playbackDataStore: DataStore<Preferences> by preferencesDataStore(
    name = "playback_prefs",
)

/**
 * User-tunable behaviour around metered networks. Two knobs today:
 *
 *  - download_on_wifi_only — DownloadManager swaps NetworkType.CONNECTED
 *    for NetworkType.UNMETERED on enqueue, so WorkManager defers any
 *    queued download until the device is on Wi-Fi (or unmetered
 *    Ethernet). Default on: a 4 GB movie download over LTE is the
 *    kind of thing a user notices on their bill.
 *
 *  - warn_on_cellular_stream — PlayerScreen shows a confirm dialog
 *    before starting a video stream over a metered connection. User
 *    acks once per playback session. Default on for the same reason.
 *
 * Both default to "protective" so a freshly-installed app doesn't
 * burn the user's data plan before they've found the settings page.
 */
class PlaybackPrefs(private val context: Context) {

    companion object {
        private val KEY_DOWNLOAD_WIFI_ONLY = booleanPreferencesKey("download_wifi_only")
        private val KEY_WARN_CELLULAR_STREAM = booleanPreferencesKey("warn_cellular_stream")
    }

    val downloadOnWifiOnly: Flow<Boolean> = context.playbackDataStore.data.map {
        it[KEY_DOWNLOAD_WIFI_ONLY] ?: true
    }

    val warnOnCellularStream: Flow<Boolean> = context.playbackDataStore.data.map {
        it[KEY_WARN_CELLULAR_STREAM] ?: true
    }

    suspend fun getDownloadOnWifiOnly(): Boolean = downloadOnWifiOnly.first()
    suspend fun getWarnOnCellularStream(): Boolean = warnOnCellularStream.first()

    suspend fun setDownloadOnWifiOnly(value: Boolean) {
        context.playbackDataStore.edit { it[KEY_DOWNLOAD_WIFI_ONLY] = value }
    }

    suspend fun setWarnOnCellularStream(value: Boolean) {
        context.playbackDataStore.edit { it[KEY_WARN_CELLULAR_STREAM] = value }
    }
}
