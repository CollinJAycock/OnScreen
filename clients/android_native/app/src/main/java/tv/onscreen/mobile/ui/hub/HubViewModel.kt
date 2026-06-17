package tv.onscreen.mobile.ui.hub

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.model.HubData
import tv.onscreen.mobile.data.model.HubRowPref
import tv.onscreen.mobile.data.model.Library
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.HubRepository
import tv.onscreen.mobile.data.repository.LibraryRepository
import tv.onscreen.mobile.data.repository.PreferencesRepository
import javax.inject.Inject

@HiltViewModel
class HubViewModel @Inject constructor(
    private val hubRepo: HubRepository,
    private val libraryRepo: LibraryRepository,
    private val prefs: ServerPrefs,
    private val prefsRepo: PreferencesRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(HubUi())
    val state: StateFlow<HubUi> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            _state.value = _state.value.copy(loading = true, error = null)
            try {
                val hub = hubRepo.getHub()
                val libs = libraryRepo.getLibraries()
                val serverUrl = prefs.getServerUrl().orEmpty()
                // Best-effort: a prefs failure must not blank the home screen —
                // fall back to the default (empty) layout.
                val layout = try {
                    prefsRepo.get().hub_layout ?: emptyList()
                } catch (_: Exception) {
                    emptyList()
                }
                _state.value = HubUi(
                    loading = false,
                    hub = hub,
                    libraries = libs,
                    serverUrl = serverUrl,
                    hubLayout = layout,
                )
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false, error = e.message)
            }
        }
    }
}

data class HubUi(
    val loading: Boolean = true,
    val hub: HubData? = null,
    val libraries: List<Library> = emptyList(),
    val serverUrl: String = "",
    // User's saved hub row order + visibility (from web). Empty = default layout.
    val hubLayout: List<HubRowPref> = emptyList(),
    val error: String? = null,
)
