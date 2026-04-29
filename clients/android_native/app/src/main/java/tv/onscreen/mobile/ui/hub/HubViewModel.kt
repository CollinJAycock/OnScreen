package tv.onscreen.mobile.ui.hub

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.model.HubData
import tv.onscreen.mobile.data.model.Library
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.HubRepository
import tv.onscreen.mobile.data.repository.LibraryRepository
import javax.inject.Inject

@HiltViewModel
class HubViewModel @Inject constructor(
    private val hubRepo: HubRepository,
    private val libraryRepo: LibraryRepository,
    private val prefs: ServerPrefs,
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
                _state.value = HubUi(
                    loading = false,
                    hub = hub,
                    libraries = libs,
                    serverUrl = serverUrl,
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
    val error: String? = null,
)
