package tv.onscreen.mobile.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.repository.AuthRepository
import javax.inject.Inject

/** UI state for the 2FA enrolment flow on the phone. */
sealed interface SecurityUiState {
    data object Loading : SecurityUiState
    data class Disabled(val error: String? = null) : SecurityUiState
    data class Enrolling(
        val secret: String,
        val qrPngBase64: String?,
        val error: String? = null,
    ) : SecurityUiState
    data class Recovery(val codes: List<String>) : SecurityUiState
    data class Enabled(val recoveryRemaining: Int, val error: String? = null) : SecurityUiState
}

@HiltViewModel
class SecurityViewModel @Inject constructor(
    private val auth: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<SecurityUiState>(SecurityUiState.Loading)
    val state: StateFlow<SecurityUiState> = _state.asStateFlow()

    init { loadStatus() }

    fun loadStatus() {
        viewModelScope.launch {
            _state.value = try {
                val s = auth.totpStatus()
                if (s.enabled) SecurityUiState.Enabled(s.recovery_codes_remaining)
                else SecurityUiState.Disabled()
            } catch (e: Exception) {
                SecurityUiState.Disabled(e.message)
            }
        }
    }

    fun startEnrol() {
        viewModelScope.launch {
            _state.value = try {
                val s = auth.totpSetup()
                SecurityUiState.Enrolling(secret = s.secret, qrPngBase64 = s.qr_png)
            } catch (e: Exception) {
                SecurityUiState.Disabled(e.message ?: "Could not start setup")
            }
        }
    }

    fun confirm(code: String) {
        val cur = _state.value as? SecurityUiState.Enrolling ?: return
        viewModelScope.launch {
            _state.value = try {
                SecurityUiState.Recovery(auth.totpActivate(code.trim()).recovery_codes)
            } catch (e: Exception) {
                cur.copy(error = e.message ?: "That code didn't match")
            }
        }
    }

    /** Called after the user acknowledges they saved their recovery
     *  codes — re-loads status to land on the Enabled view. */
    fun finishEnrol() = loadStatus()

    fun disable(code: String) {
        val cur = _state.value as? SecurityUiState.Enabled ?: return
        viewModelScope.launch {
            try {
                auth.totpDisable(code.trim())
                loadStatus()
            } catch (e: Exception) {
                _state.value = cur.copy(error = e.message ?: "That code didn't match")
            }
        }
    }
}
