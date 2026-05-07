package tv.onscreen.mobile.data.repository

import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.AuthProviders
import tv.onscreen.mobile.data.model.AuthProviderStatus
import tv.onscreen.mobile.data.model.LoginRequest
import tv.onscreen.mobile.data.model.LogoutRequest
import tv.onscreen.mobile.data.model.PairCodeResponse
import tv.onscreen.mobile.data.model.TokenPair
import tv.onscreen.mobile.data.prefs.ServerPrefs
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
open class AuthRepository @Inject constructor(
    private val api: OnScreenApi,
    private val prefs: ServerPrefs,
) {
    open suspend fun login(username: String, password: String): TokenPair {
        val pair = api.login(LoginRequest(username, password)).data
        prefs.setTokens(pair.access_token, pair.refresh_token)
        prefs.setUser(pair.user_id, pair.username)
        return pair
    }

    /** LDAP login — same shape as local but hits the alternate
     *  endpoint. Server JIT-provisions a user row on first success
     *  and returns the same TokenPair. Persistence side-effect mirrors
     *  [login] so the UI is unchanged downstream. */
    open suspend fun loginLdap(username: String, password: String): TokenPair {
        val pair = api.loginLdap(LoginRequest(username, password)).data
        prefs.setTokens(pair.access_token, pair.refresh_token)
        prefs.setUser(pair.user_id, pair.username)
        return pair
    }

    /** Discover which federated auth providers the server has
     *  enabled. Fans out the three /enabled endpoints in parallel —
     *  each is independent and one failing shouldn't fail the rest.
     *  Failed individual probes return null in the aggregate so the
     *  UI hides the corresponding row instead of misreporting. */
    open suspend fun getAuthProviders(): AuthProviders = coroutineScope {
        val ldapDeferred = async { safeProbe { api.getLdapEnabled().data } }
        val oidcDeferred = async { safeProbe { api.getOidcEnabled().data } }
        val samlDeferred = async { safeProbe { api.getSamlEnabled().data } }
        AuthProviders(
            ldap = ldapDeferred.await(),
            oidc = oidcDeferred.await(),
            saml = samlDeferred.await(),
        )
    }

    private suspend fun safeProbe(block: suspend () -> AuthProviderStatus): AuthProviderStatus? =
        try { block() } catch (_: Exception) { null }

    /** Current server URL (no trailing slash). Used by the SSO bridge
     *  to build the Custom-Tabs target URL. Returns null when not yet
     *  set — caller skips the launch. */
    open suspend fun getServerUrl(): String? = prefs.getServerUrl()?.trimEnd('/')

    suspend fun logout() {
        try {
            val refreshToken = prefs.getRefreshToken()
            if (!refreshToken.isNullOrEmpty()) {
                api.logout(LogoutRequest(refreshToken))
            }
        } catch (_: Exception) {
            // Best-effort — server may be unreachable.
        }
        prefs.clearAuth()
    }

    /** Result of [checkServer]. Carries the actual failure reason so
     *  the UI can show "DNS could not resolve" vs "TLS handshake
     *  failed" vs "HTTP 503" instead of the generic "server
     *  unreachable" — that masking ate too many round-trips of "is
     *  the server up? is the URL right? is wifi working?" the last
     *  time someone hit a first-launch failure. */
    sealed class CheckResult {
        data object Ok : CheckResult()
        data class HttpError(val code: Int) : CheckResult()
        data class Failed(val message: String) : CheckResult()
    }

    /** Check server reachability by hitting the health endpoint. */
    suspend fun checkServer(url: String): CheckResult {
        prefs.setServerUrl(url)
        return try {
            val resp = api.healthCheck()
            if (resp.isSuccessful) CheckResult.Ok
            else CheckResult.HttpError(resp.code())
        } catch (e: Exception) {
            CheckResult.Failed(e.javaClass.simpleName + ": " + (e.message ?: "unknown"))
        }
    }

    // ── Device pairing ────────────────────────────────────────────────────────

    /** Start a device-pairing session. The PIN is shown on the TV
     *  while the user signs in via /pair on a phone / laptop. */
    suspend fun startPairing(): PairCodeResponse =
        api.createPairCode().data

    /** One poll tick. Returns:
     *   - [PollResult.Pending] while the server says 202 (user hasn't
     *     signed in or hasn't typed the PIN yet)
     *   - [PollResult.Done] with the issued TokenPair on 200 — caller
     *     persists tokens via [completePairing]
     *   - [PollResult.Expired] on 410 (TTL elapsed without claim, or
     *     someone tried to redeem the same device_token twice)
     *   - [PollResult.Failure] on any other error so the UI can fall
     *     back to "couldn't reach server, will keep trying"
     */
    suspend fun pollPairing(deviceToken: String): PollResult {
        val resp = try {
            api.pollPairCode("Bearer $deviceToken")
        } catch (e: Exception) {
            return PollResult.Failure(e.message ?: "network")
        }
        return when (resp.code()) {
            200 -> {
                val pair = resp.body()?.data
                if (pair == null) PollResult.Failure("empty body")
                else PollResult.Done(pair)
            }
            202 -> PollResult.Pending
            410 -> PollResult.Expired
            else -> PollResult.Failure("HTTP ${resp.code()}")
        }
    }

    /** Persist the pair-issued tokens — symmetric with [login]'s
     *  side effect so the pairing UI lands the user in the same
     *  signed-in state a password login would. */
    suspend fun completePairing(pair: TokenPair) {
        prefs.setTokens(pair.access_token, pair.refresh_token)
        prefs.setUser(pair.user_id, pair.username)
    }

    sealed class PollResult {
        data object Pending : PollResult()
        data object Expired : PollResult()
        data class Done(val pair: TokenPair) : PollResult()
        data class Failure(val reason: String) : PollResult()
    }
}
