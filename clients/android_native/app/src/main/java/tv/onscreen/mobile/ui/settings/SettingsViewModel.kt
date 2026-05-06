package tv.onscreen.mobile.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.prefs.PlaybackPrefs
import javax.inject.Inject

@HiltViewModel
class SettingsViewModel @Inject constructor(
    private val prefs: PlaybackPrefs,
) : ViewModel() {

    val downloadOnWifiOnly: Flow<Boolean> = prefs.downloadOnWifiOnly
    val warnOnCellularStream: Flow<Boolean> = prefs.warnOnCellularStream

    fun setDownloadOnWifiOnly(value: Boolean) {
        viewModelScope.launch { prefs.setDownloadOnWifiOnly(value) }
    }

    fun setWarnOnCellularStream(value: Boolean) {
        viewModelScope.launch { prefs.setWarnOnCellularStream(value) }
    }
}
