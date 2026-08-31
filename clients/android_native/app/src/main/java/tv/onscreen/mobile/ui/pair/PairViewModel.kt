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
import tv.onscreen.mobile.data.model.AuthProviders
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

    /** The persisted server URL, when one exists. Prefills the entry
     *  field when auto-resume can't complete (server unreachable) and
     *  labels the "Change server" affordance on the sign-in step. */
    private val _knownServerUrl = MutableStateFlow<String?>(null)
    val knownServerUrl: StateFlow<String?> = _knownServerUrl.asStateFlow()

    init {
        // Sign-out deliberately KEEPS the server URL — the Settings copy
        // promises "the server URL is kept so you can sign in again". This
        // VM used to open at NeedsServer regardless, so the kept URL was
        // invisible and the user retyped it: on-device, sign-out was
        // indistinguishable from Forget server. Resume the stored URL and
        // jump straight to the sign-in step; a failed probe falls back to
        // the entry screen with the URL prefilled instead of blank.
        viewModelScope.launch {
            val stored = authRepo.getServerUrl()
            if (stored.isNullOrEmpty()) return@launch // true first run
            _knownServerUrl.value = stored
            submitServerUrl(stored)
        }
    }

    /** "Change server" on the sign-in step: back to the entry screen
     *  (prefilled from [knownServerUrl]) without touching stored state —
     *  nothing is forgotten unless the user submits a different URL. */
    fun editServer() {
        pollJob?.cancel()
        _state.value = PairState.NeedsServer
    }

    /** Discovered auth-provider availability for the currently-bound
     *  server. Null until the server is reachable; the choice screen
     *  reads this to decide whether to show the LDAP toggle and the
     *  "use SSO via web" hint. */
    private val _providers = MutableStateFlow<AuthProviders?>(null)
    val providers: StateFlow<AuthProviders?> = _providers.asStateFlow()

    private var pollJob: Job? = null

    fun submitServerUrl(url: String) {
        if (url.isBlank()) return
        val normalized = normalizeServerUrl(url)
        viewModelScope.launch {
            _state.value = PairState.CheckingServer
            when (val result = authRepo.checkServer(normalized)) {
                is AuthRepository.CheckResult.Ok -> {
                    _state.value = PairState.ServerReady()
                    // Fan out the three /enabled probes immediately —
                    // best-effort, no failure path. UI just won't show
                    // the federated rows if the providers blob doesn't
                    // arrive, falling back to the local-only login form.
                    _providers.value = try { authRepo.getAuthProviders() } catch (_: Exception) { null }
                }
                is AuthRepository.CheckResult.HttpError -> {
                    _state.value = PairState.Error("Server returned HTTP ${result.code} from $normalized")
                }
                is AuthRepository.CheckResult.Failed -> {
                    _state.value = PairState.Error("Couldn't reach $normalized — ${result.message}")
                }
            }
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
                val pair = authRepo.login(username, password)
                _state.value = if (pair.totp_required) {
                    PairState.TotpRequired(pair.login_challenge_token.orEmpty())
                } else {
                    PairState.Done
                }
            } catch (e: Exception) {
                // Stay on the choice screen (the server is fine; the
                // credentials were wrong) so the user keeps their typed
                // server URL and login fields and only sees an inline error.
                _state.value = PairState.ServerReady(e.message ?: "login failed")
            }
        }
    }

    /** Second step of a two-factor login. On a wrong code we stay in the
     *  TotpRequired state (with an inline error) so the user can retry
     *  without re-entering their password. */
    fun verifyTotp(challengeToken: String, code: String) {
        viewModelScope.launch {
            _state.value = PairState.LoggingIn
            try {
                authRepo.verifyTotp(challengeToken, code)
                _state.value = PairState.Done
            } catch (e: Exception) {
                _state.value = PairState.TotpRequired(challengeToken, e.message ?: "verification failed")
            }
        }
    }

    /** LDAP login — same shape as [loginWithPassword] but hits the
     *  LDAP endpoint. Server JIT-provisions a user row on first
     *  successful bind. UI gates the call site behind
     *  [providers].ldapEnabled. */
    fun loginWithLdap(username: String, password: String) {
        viewModelScope.launch {
            _state.value = PairState.LoggingIn
            try {
                authRepo.loginLdap(username, password)
                _state.value = PairState.Done
            } catch (e: Exception) {
                // Keep the user on the sign-in choice screen with their
                // entered fields intact (server reachability is unchanged).
                _state.value = PairState.ServerReady(e.message ?: "LDAP login failed")
            }
        }
    }

    /** SSO bridge launch URL — set by [startSsoBridge] when a Custom
     *  Tabs invocation is queued. The PairScreen consumes it from a
     *  LaunchedEffect and clears via [consumeSsoLaunchUrl] so a
     *  configuration change doesn't re-open the browser twice. */
    private val _ssoLaunchUrl = MutableStateFlow<String?>(null)
    val ssoLaunchUrl: StateFlow<String?> = _ssoLaunchUrl.asStateFlow()

    /**
     * Start the SSO bridge: request a pair code, expose the
     * Custom-Tabs URL via [ssoLaunchUrl], and start polling. The
     * web /pair?code=…&auto=1 page auto-claims when the user is
     * signed in (via local / LDAP / OIDC / SAML), and the next
     * poll tick lands the token pair on the device.
     */
    fun startSsoBridge(deviceName: String = "Phone") {
        viewModelScope.launch {
            _state.value = PairState.RequestingCode
            try {
                val serverUrl = authRepo.getServerUrl()
                if (serverUrl.isNullOrEmpty() || !SsoBridge.isLaunchableServerUrl(serverUrl)) {
                    _state.value = PairState.Error("server URL not set — check connection")
                    return@launch
                }
                val code = authRepo.startPairing()
                val url = SsoBridge.buildPairUrl(serverUrl, code.pin, deviceName)
                _state.value = PairState.WaitingForClaim(code.pin)
                _ssoLaunchUrl.value = url
                beginPolling(code.device_token)
            } catch (e: Exception) {
                _state.value = PairState.Error(e.message ?: "couldn't start SSO sign-in")
            }
        }
    }

    fun consumeSsoLaunchUrl() {
        _ssoLaunchUrl.value = null
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

/**
 * Pick a scheme for a bare-host URL the user typed into the server
 * field. Inputs that already carry a scheme are passed through as-is.
 *
 * Heuristic: hosts that look like RFC1918 / loopback / `.local` /
 * `localhost` default to `http://` because the typical HomeLab box
 * isn't running TLS. Everything else defaults to `https://` because a
 * public DNS name almost always has a cert (Cloudflare tunnels,
 * Tailscale Funnel, Let's Encrypt). This stops the "I typed
 * 192.168.1.50:7070 and got a confusing TLS error" case without
 * surprising remote users whose servers are HTTPS-only.
 *
 * Internal (not private) so the unit suite can exercise the
 * heuristic without standing up a ViewModel.
 */
internal fun normalizeServerUrl(raw: String): String {
    val trimmed = raw.trim()
    if (trimmed.startsWith("http://", ignoreCase = true) ||
        trimmed.startsWith("https://", ignoreCase = true)
    ) {
        return trimmed
    }
    // Strip path / port to isolate the host portion. `host:port/path`
    // → `host`. IPv6 literals would arrive bracketed (`[::1]:7070`)
    // but that path is unusual enough to leave to the user.
    val host = trimmed
        .substringBefore('/')
        .substringBefore(':')
        .lowercase()
    val isPrivate = host == "localhost" ||
        host.endsWith(".local") ||
        host.matches(Regex("""^127\.\d+\.\d+\.\d+$""")) ||
        host.matches(Regex("""^10\.\d+\.\d+\.\d+$""")) ||
        host.matches(Regex("""^192\.168\.\d+\.\d+$""")) ||
        host.matches(Regex("""^172\.(1[6-9]|2\d|3[01])\.\d+\.\d+$""")) ||
        host == "::1"
    val scheme = if (isPrivate) "http" else "https"
    return "$scheme://$trimmed"
}

sealed class PairState {
    data object NeedsServer : PairState()
    data object CheckingServer : PairState()
    data object ServerUnreachable : PairState()
    // Server reachable; showing the sign-in choice screen. `loginError`
    // surfaces a failed password/LDAP attempt inline without dropping
    // the user back to the server-URL prompt or clearing their fields.
    data class ServerReady(val loginError: String? = null) : PairState()
    data object RequestingCode : PairState()
    data object LoggingIn : PairState()
    data class WaitingForClaim(val code: String) : PairState()
    // Password accepted, second factor owed. Carries the challenge token
    // the verify step posts back; `error` surfaces a wrong-code retry
    // without dropping out of the TOTP step.
    data class TotpRequired(val challengeToken: String, val error: String? = null) : PairState()
    data object Done : PairState()
    data class Error(val message: String) : PairState()
}
