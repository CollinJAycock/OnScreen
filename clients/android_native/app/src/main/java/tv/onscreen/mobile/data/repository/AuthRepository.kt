package tv.onscreen.mobile.data.repository

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import tv.onscreen.mobile.data.api.AuthInterceptor
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.AuthProviders
import tv.onscreen.mobile.data.model.AuthProviderStatus
import tv.onscreen.mobile.data.model.LoginRequest
import tv.onscreen.mobile.data.model.LogoutRequest
import tv.onscreen.mobile.data.model.PairCodeResponse
import tv.onscreen.mobile.data.model.TokenPair
import tv.onscreen.mobile.data.model.TotpActivateResponse
import tv.onscreen.mobile.data.model.TotpCodeRequest
import tv.onscreen.mobile.data.model.TotpSetupResponse
import tv.onscreen.mobile.data.model.TotpStatusResponse
import tv.onscreen.mobile.data.model.TotpVerifyRequest
import tv.onscreen.mobile.data.prefs.ServerPrefs
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
open class AuthRepository @Inject constructor(
    private val api: OnScreenApi,
    private val prefs: ServerPrefs,
    // Process-lifetime @Singleton cache keyed to nothing. Every identity
    // transition below has to clear it or the next user on this phone
    // inherits the previous one's hub layout, language preferences and
    // content-rating ceiling. Mirrors the TV client's
    // invalidateIdentityCaches(). PreferencesRepository doesn't depend on
    // AuthRepository, so there's no Hilt cycle.
    private val preferences: PreferencesRepository,
    // The bearer cache has a 5 s TTL for throughput; an identity change must
    // drop it immediately or the requests right after sign-in carry the
    // PREVIOUS user's token (and right after sign-out, a revoked one).
    private val authInterceptor: AuthInterceptor,
) {
    /** App-lifetime scope for teardown writes that must outlive the caller —
     *  see [logoutDetached]. Never cancelled. */
    private val detachedScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /** Clear per-identity caches. Called from every sign-in, sign-out and
     *  pairing completion — the points where "who is signed in" changes. */
    private fun invalidateIdentityCaches() {
        preferences.invalidate()
        authInterceptor.invalidateCache()
    }

    open suspend fun login(username: String, password: String): TokenPair {
        ensureTlsUpgradedOrigin()
        val pair = api.login(LoginRequest(username, password)).data
        persistIfComplete(pair)
        return pair
    }

    /** LDAP login — same shape as local but hits the alternate
     *  endpoint. Server JIT-provisions a user row on first success
     *  and returns the same TokenPair. Persistence side-effect mirrors
     *  [login] so the UI is unchanged downstream. */
    open suspend fun loginLdap(username: String, password: String): TokenPair {
        ensureTlsUpgradedOrigin()
        val pair = api.loginLdap(LoginRequest(username, password)).data
        persistIfComplete(pair)
        return pair
    }

    /** Second step of a two-factor login: exchange the challenge token +
     *  code (or recovery code) for a real token pair, then persist it. */
    open suspend fun verifyTotp(challengeToken: String, code: String): TokenPair {
        val pair = api.verifyTotp(TotpVerifyRequest(challengeToken, code)).data
        persistIfComplete(pair)
        return pair
    }

    /** A TOTP-gated login returns totp_required with no tokens — don't
     *  persist empties; the caller drives the second factor. */
    private suspend fun persistIfComplete(pair: TokenPair) {
        if (pair.totp_required) return
        invalidateIdentityCaches()
        prefs.setTokens(pair.access_token, pair.refresh_token, pair.asset_token)
        prefs.setUser(pair.user_id, pair.username)
    }

    // ── 2FA self-management ───────────────────────────────────────────────────
    open suspend fun totpStatus(): TotpStatusResponse = api.totpStatus().data
    open suspend fun totpSetup(): TotpSetupResponse = api.totpSetup().data
    open suspend fun totpActivate(code: String): TotpActivateResponse =
        api.totpActivate(TotpCodeRequest(code)).data
    open suspend fun totpDisable(code: String): TotpStatusResponse =
        api.totpDisable(TotpCodeRequest(code)).data

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
        invalidateIdentityCaches()
        prefs.clearAuth()
    }

    /** Fire-and-forget [logout] on the app-lifetime scope, with an optional
     *  follow-up that runs only after the revocation attempt completes.
     *
     *  Sign-out flips `isLoggedIn`, which makes AppNav rebuild the graph and
     *  clear the settings ViewModel — a viewModelScope.launch would be
     *  cancelled before the revoke POST lands (the same trap ItemRepository
     *  documents for the terminal progress event). */
    fun logoutDetached(andThen: (suspend () -> Unit)? = null) {
        detachedScope.launch {
            logout()
            andThen?.invoke()
        }
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

    /** Check server reachability by hitting the health endpoint.
     *
     *  Adopts the origin the server actually ANSWERED on. The setup screen
     *  defaults private hosts (and any address typed with an explicit
     *  `http://`) to cleartext, and this probe is a safe GET that OkHttp
     *  happily follows across a 301 to TLS — so the check "succeeds" while
     *  the PERSISTED origin stays http. Every later POST then dies: a 301
     *  makes OkHttp rewrite POST→GET per RFC, the API 405s, and login /
     *  pairing / token refresh fail with no useful error. Adopting the
     *  answered origin here is what makes the stored value truthful.
     *
     *  Upgrade-only and same-host: a hostile redirect from https down to
     *  http, or off to another host, is ignored. */
    suspend fun checkServer(url: String): CheckResult {
        prefs.setServerUrl(url)
        return try {
            val resp = api.healthCheck()
            if (resp.isSuccessful) {
                adoptRedirectedOrigin(url, resp.raw().request.url)
                CheckResult.Ok
            } else CheckResult.HttpError(resp.code())
        } catch (e: Exception) {
            CheckResult.Failed(e.javaClass.simpleName + ": " + (e.message ?: "unknown"))
        }
    }

    /** Persist [finalUrl]'s origin when a request was upgraded to TLS. */
    private suspend fun adoptRedirectedOrigin(requested: String?, finalUrl: HttpUrl) {
        val asked = requested?.toHttpUrlOrNull() ?: return
        if (asked.isHttps || !finalUrl.isHttps) return
        if (!asked.host.equals(finalUrl.host, ignoreCase = true)) return
        prefs.setServerUrl("https://" + finalUrl.host + portSuffix(finalUrl))
    }

    private fun portSuffix(u: HttpUrl): String =
        if (u.port == HttpUrl.defaultPort(u.scheme)) "" else ":" + u.port

    /** Upgrade a stale cleartext origin to TLS before an unauthenticated
     *  POST. [checkServer] adopts the answered origin during setup, but an
     *  install that persisted http BEFORE that fix landed — or whose server
     *  moved behind TLS later (reverse proxy, Tailscale cert) — never
     *  re-runs setup, so the stored origin stays cleartext for life and
     *  every login/pairing POST 405s. The probe is a safe GET and reuses
     *  the same upgrade-only, same-host adoption; a plain-http LAN server
     *  answers without a redirect and nothing changes. Failures are
     *  swallowed — the caller's own request reports reachability. */
    private suspend fun ensureTlsUpgradedOrigin() {
        val stored = prefs.getServerUrl()?.toHttpUrlOrNull() ?: return
        if (stored.isHttps) return
        try {
            val resp = api.healthCheck()
            if (resp.isSuccessful) {
                adoptRedirectedOrigin(prefs.getServerUrl(), resp.raw().request.url)
            }
        } catch (_: Exception) {
            // Unreachable — let the caller's request surface it.
        }
    }

    // ── Device pairing ────────────────────────────────────────────────────────

    /** Start a device-pairing session. The PIN is shown on the TV
     *  while the user signs in via /pair on a phone / laptop. */
    suspend fun startPairing(): PairCodeResponse {
        ensureTlsUpgradedOrigin()
        return api.createPairCode().data
    }

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
        invalidateIdentityCaches()
        prefs.setTokens(pair.access_token, pair.refresh_token, pair.asset_token)
        prefs.setUser(pair.user_id, pair.username)
    }

    sealed class PollResult {
        data object Pending : PollResult()
        data object Expired : PollResult()
        data class Done(val pair: TokenPair) : PollResult()
        data class Failure(val reason: String) : PollResult()
    }
}
