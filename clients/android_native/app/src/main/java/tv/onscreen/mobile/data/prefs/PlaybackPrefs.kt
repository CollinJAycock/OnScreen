package tv.onscreen.mobile.data.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.doublePreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import tv.onscreen.mobile.playback.ReplayGain
import tv.onscreen.mobile.playback.ReplayGainMode

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
 *
 * Plus the music player's ReplayGain (Settings → Playback):
 *
 *  - replaygain_mode — off / track / album (album falls back to track tags
 *    on files without album gain). Default OFF: nothing about playback
 *    changes until the user opts in.
 *  - replaygain_preamp_db — added to the tag gain, -6..+6 dB in 0.5 dB
 *    steps, default 0. Peak-capped by the gain math, so it can't clip.
 *
 * Applied by PlaybackService's audio stage — see
 * [tv.onscreen.mobile.playback.ReplayGainAudioProcessor].
 */
class PlaybackPrefs(private val context: Context) {

    companion object {
        private val KEY_DOWNLOAD_WIFI_ONLY = booleanPreferencesKey("download_wifi_only")
        private val KEY_WARN_CELLULAR_STREAM = booleanPreferencesKey("warn_cellular_stream")
        private val KEY_REPLAYGAIN_MODE = stringPreferencesKey("replaygain_mode")
        private val KEY_REPLAYGAIN_PREAMP_DB = doublePreferencesKey("replaygain_preamp_db")
    }

    val downloadOnWifiOnly: Flow<Boolean> = context.playbackDataStore.data.map {
        it[KEY_DOWNLOAD_WIFI_ONLY] ?: true
    }

    val warnOnCellularStream: Flow<Boolean> = context.playbackDataStore.data.map {
        it[KEY_WARN_CELLULAR_STREAM] ?: true
    }

    val replayGainMode: Flow<ReplayGainMode> = context.playbackDataStore.data.map {
        ReplayGainMode.fromWire(it[KEY_REPLAYGAIN_MODE])
    }

    /** Always on the settings grid (-6..+6, 0.5 dB steps) — a hand-edited or
     *  future out-of-range value is snapped rather than trusted. */
    val replayGainPreampDb: Flow<Double> = context.playbackDataStore.data.map {
        ReplayGain.snapUiPreamp(it[KEY_REPLAYGAIN_PREAMP_DB] ?: 0.0)
    }

    suspend fun getDownloadOnWifiOnly(): Boolean = downloadOnWifiOnly.first()
    suspend fun getWarnOnCellularStream(): Boolean = warnOnCellularStream.first()

    suspend fun setDownloadOnWifiOnly(value: Boolean) {
        context.playbackDataStore.edit { it[KEY_DOWNLOAD_WIFI_ONLY] = value }
    }

    suspend fun setWarnOnCellularStream(value: Boolean) {
        context.playbackDataStore.edit { it[KEY_WARN_CELLULAR_STREAM] = value }
    }

    suspend fun setReplayGainMode(mode: ReplayGainMode) {
        context.playbackDataStore.edit { it[KEY_REPLAYGAIN_MODE] = mode.wire }
    }

    suspend fun setReplayGainPreampDb(db: Double) {
        context.playbackDataStore.edit { it[KEY_REPLAYGAIN_PREAMP_DB] = ReplayGain.snapUiPreamp(db) }
    }
}
