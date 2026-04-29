package tv.onscreen.mobile.ui.pair

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.repository.AuthRepository
import javax.inject.Inject

// Phone-side pairing flow uses the same /auth/pair endpoints the
// TV clients use. The phone is *also* a paired device — operator
// can sign in once via the web at /pair on a laptop, claim the PIN,
// and the phone gets a token pair without ever asking for a password.
// This is the standard auth path here; password login is offered as a
// fallback (an embedded form lower on the screen).
@HiltViewModel
class PairViewModel @Inject constructor(
    private val authRepo: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<PairState>(PairState.NeedsServer)
    val state: StateFlow<PairState> = _state.asStateFlow()

    private var pollJob: Job? = null

    fun submitServerUrl(url: String) {
        if (url.isBlank()) return
        val normalized = url.trim().let { if (it.startsWith("http")) it else "https://$it" }
        viewModelScope.launch {
            _state.value = PairState.CheckingServer
            val ok = authRepo.checkServer(normalized)
            _state.value = if (ok) PairState.ServerReady else PairState.ServerUnreachable
        }
    }

    fun startPairing() {
        viewModelScope.launch {
            _state.value = PairState.RequestingCode
            try {
                val code = authRepo.startPairing()
                _state.value = PairState.WaitingForClaim(code.pin)
                beginPolling(code.device_token)
            } catch (e: Exception) {
                _state.value = PairState.Error(e.message ?: "couldn't start pairing")
            }
        }
    }

    fun loginWithPassword(username: String, password: String) {
        viewModelScope.launch {
            _state.value = PairState.LoggingIn
            try {
                authRepo.login(username, password)
                _state.value = PairState.Done
            } catch (e: Exception) {
                _state.value = PairState.Error(e.message ?: "login failed")
            }
        }
    }

    private fun beginPolling(deviceToken: String) {
        pollJob?.cancel()
        pollJob = viewModelScope.launch {
            while (true) {
                when (val r = authRepo.pollPairing(deviceToken)) {
                    is AuthRepository.PollResult.Done -> {
                        authRepo.completePairing(r.pair)
                        _state.value = PairState.Done
                        return@launch
                    }
                    AuthRepository.PollResult.Expired -> {
                        _state.value = PairState.Error("pairing code expired — try again")
                        return@launch
                    }
                    AuthRepository.PollResult.Pending -> Unit
                    is AuthRepository.PollResult.Failure -> Unit
                }
                delay(2000)
            }
        }
    }

    override fun onCleared() {
        pollJob?.cancel()
        super.onCleared()
    }

    fun reset() {
        pollJob?.cancel()
        _state.value = PairState.NeedsServer
    }
}

sealed class PairState {
    data object NeedsServer : PairState()
    data object CheckingServer : PairState()
    data object ServerUnreachable : PairState()
    data object ServerReady : PairState()
    data object RequestingCode : PairState()
    data object LoggingIn : PairState()
    data class WaitingForClaim(val code: String) : PairState()
    data object Done : PairState()
    data class Error(val message: String) : PairState()
}
