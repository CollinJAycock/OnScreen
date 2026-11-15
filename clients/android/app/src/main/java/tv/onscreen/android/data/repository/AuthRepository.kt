package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.AuthInterceptor
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.LoginRequest
import tv.onscreen.android.data.model.LogoutRequest
import tv.onscreen.android.data.model.PairCodeResponse
import tv.onscreen.android.data.model.RefreshRequest
import tv.onscreen.android.data.model.TokenPair
import tv.onscreen.android.data.model.TotpVerifyRequest
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.playback.StreamTokenVault
import tv.onscreen.android.data.backgroundScope
import tv.onscreen.android.di.AuthClient
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class AuthRepository @Inject constructor(
    private val api: OnScreenApi,
    // The plain auth client (no AuthInterceptor, no TokenAuthenticator) for
    // the detached sign-out revoke: it runs after local auth is cleared, and
    // the main client's authenticator answered a 401 there by retrying with
    // whatever token is stored by then — a NEW sign-in's, if one landed.
    @AuthClient private val authApi: OnScreenApi,
    private val prefs: ServerPrefs,
    // Process-lifetime @Singleton caches that are keyed to nothing. Every
    // identity transition below has to clear them, or the next user on a shared
    // TV inherits the previous one's state. Neither depends on AuthRepository,
    // so there is no Hilt cycle.
    private val preferences: PreferencesRepository,
    private val capabilities: CapabilitiesRepository,
    private val notifications: NotificationsRepository,
    // Bearer cache with a 5 s TTL. Every token write here (sign-in, pairing,
    // sign-out) has to drop it, or the next requests carry the token from
    // before the write for the rest of the window.
    private val authInterceptor: AuthInterceptor,
) {
    /** App-lifetime scope for the sign-out revoke, which must outlive the
     *  caller (see [logout]). Never cancelled. */
    private val detachedScope = backgroundScope("AuthRepository")

    /** Clear per-identity caches. Called from every sign-in, sign-out and
     *  pairing completion — the points where "who is signed in" changes —
     *  AFTER the token write. */
    private fun invalidateIdentityCaches() {
        preferences.invalidate()
        capabilities.invalidate()
        // Re-dial the shared SSE — it authenticated with the PREVIOUS
        // user's bearer at connect time and never re-reads it. Only once the
        // new tokens are stored: this used to run first, so the re-dial could
        // go out bare, and the 401 sent TokenAuthenticator to clear auth
        // (it read no refresh token yet) right after the sign-in's write —
        // bouncing a fresh sign-in back to Login.
        notifications.restartForIdentityChange()
    }

    suspend fun login(username: String, password: String): TokenPair {
        ensureTlsUpgradedOrigin()
        val pair = api.login(LoginRequest(username, password)).data
        persistIfComplete(pair)
        return pair
    }

    /** Second step of a two-factor login: challenge token + code (or
     *  recovery code) -> real token pair. */
    suspend fun verifyTotp(challengeToken: String, code: String): TokenPair {
        val pair = api.verifyTotp(TotpVerifyRequest(challengeToken, code)).data
        persistIfComplete(pair)
        return pair
    }

    /** A TOTP-gated login returns totp_required with no tokens — don't
     *  persist empties; the caller drives the second factor. */
    private suspend fun persistIfComplete(pair: TokenPair) {
        if (pair.totp_required) return
        prefs.setTokens(pair.access_token, pair.refresh_token, pair.asset_token)
        prefs.setUser(pair.user_id, pair.username)
        // After the write, so the next request re-reads the token just stored
        // (the login POST itself loaded the cache with none).
        authInterceptor.invalidateCache()
        invalidateIdentityCaches()
    }

    /** Sign out: drop local identity state FIRST, then revoke server-side,
     *  detached.
     *
     *  The revoke (a refresh, then the logout POST) used to run first, on the
     *  caller's scope — Settings runs it on the activity's. With the server
     *  unreachable that is ~15 s per call with no busy state, and BACK, BACK
     *  finishes the activity on Fire OS / Android 11 and earlier: the scope
     *  was cancelled, the cancellation swallowed by the best-effort catch,
     *  and the clear below then threw at its first suspension — the tokens
     *  stayed and the next launch was still signed in (with Watch Next rows
     *  and parked audio already gone, so it LOOKED signed out). If the
     *  rotation had reached the server, the stored refresh token was also
     *  superseded, and its next use read as reuse. Clearing first makes the
     *  sign-out immediate and durable; the revoke is best-effort after, on
     *  an app-lifetime scope, with the tokens and the origin captured before
     *  the clear.
     *
     *  The revoke has to run under VALID claims or it is a silent no-op: the
     *  logout route runs under Optional auth, which never rejects, so an
     *  expired bearer yields nil claims and the handler clears cookies and
     *  answers 204 having revoked nothing — leaving the 30-day refresh session
     *  alive after a sign-out that reported success. The access token lapses
     *  after an hour and nothing refreshes it while the app idles, so
     *  [revokeDetached] rotates first and sends the resulting token
     *  explicitly. */
    suspend fun logout() {
        // The local half must not be interrupted half-way by the caller's
        // scope going away; it is a few DataStore reads and one edit.
        withContext(NonCancellable) {
            val origin = prefs.getServerUrl()?.trimEnd('/')
            val refreshToken = prefs.getRefreshToken()
            val accessToken = prefs.getAccessToken()
            prefs.clearAuth()
            // The bearer cache would otherwise keep attaching the just-revoked
            // token for up to its 5 s TTL.
            authInterceptor.invalidateCache()
            invalidateIdentityCaches()
            // In-memory stream/asset credentials the player re-attaches per
            // request.
            StreamTokenVault.clear()
            if (!refreshToken.isNullOrEmpty()) {
                detachedScope.launch { revokeDetached(origin, refreshToken, accessToken) }
            }
        }
    }

    /** The server half of [logout], with the tokens and origin it captured.
     *  Every call is pinned to [origin] by an absolute URL on the plain auth
     *  client: the user can be entering a NEW server (Change server) while a
     *  slow or dead old one times out, and the placeholder-base route
     *  re-reads the server URL per request — which would have sent the old
     *  server's tokens to the new host. */
    private suspend fun revokeDetached(origin: String?, refreshToken: String, accessToken: String?) {
        try {
            if (!pinnedOriginUsable(origin)) return
            // Refresh rotates the refresh token, so revoke the one the
            // rotation issued — replaying the superseded token would trip the
            // server's reuse detection instead of reading as a clean sign-out.
            // Fall back to the stored pair if rotation fails.
            val rotated = runCatching {
                authApi.refreshAt("$origin/api/v1/auth/refresh", RefreshRequest(refreshToken)).data
            }.getOrNull()
            val bearer = rotated?.access_token?.takeIf { it.isNotEmpty() }
                ?: accessToken?.takeIf { it.isNotEmpty() }
            val revokeToken = rotated?.refresh_token?.takeIf { it.isNotEmpty() }
                ?: refreshToken
            // Re-check right before the second send: the refresh above can
            // take the full timeout on a dead server.
            if (!pinnedOriginUsable(origin)) return
            authApi.logoutAt(
                "$origin/api/v1/auth/logout",
                LogoutRequest(revokeToken),
                bearer?.let { "Bearer $it" },
            )
        } catch (_: Exception) {
            // Best-effort — the server may be unreachable. Local state is
            // already clear, so the TV is signed out regardless.
        }
    }

    /** Whether an absolute URL on [origin] will really reach [origin].
     *  BaseUrlInterceptor leaves absolute URLs alone EXCEPT the placeholder
     *  host `localhost`, which it re-points at the CURRENT server — so a
     *  localhost origin (an emulator behind `adb reverse`) is only safe to use
     *  while it is still the configured server. Mirrors the phone client. */
    private suspend fun pinnedOriginUsable(origin: String?): Boolean {
        val url = origin?.toHttpUrlOrNull() ?: return false
        if (!url.host.equals("localhost", ignoreCase = true)) return true
        return prefs.getServerUrl()?.trimEnd('/') == origin
    }

    /** Local half of an INVOLUNTARY sign-out — TokenAuthenticator cleared the
     *  tokens because the refresh was definitively rejected (remote revoke,
     *  expiry, reuse detection). There is nothing to revoke server-side, but
     *  the same in-process identity state voluntary [logout] drops must go too,
     *  or the next person on a shared TV inherits cached prefs, a stale bearer
     *  for up to the interceptor TTL, and the previous user's stream tokens.
     *  Idempotent: every call here is a plain cache clear, so running it after
     *  a voluntary logout (whose logged-in → logged-out edge also fires the
     *  watcher) is harmless. Device surfaces (Watch Next, parked audio) are
     *  handled by SignOutTeardown, which calls this. */
    fun onInvoluntarySignOut() {
        invalidateIdentityCaches()
        authInterceptor.invalidateCache()
        StreamTokenVault.clear()
    }

    /** Check server reachability by hitting the health endpoint.
     *
     *  Adopts the origin the server actually ANSWERED on, which matters when
     *  the user typed a bare hostname. Typing `https://` on a D-pad is painful,
     *  so people enter `media.example.com`; ServerPrefs prefixes `http://`
     *  because a LAN server on plain HTTP is the dominant deployment. A
     *  TLS-serving host then 301s to https, OkHttp follows it transparently,
     *  and this check reported success — while the PERSISTED origin stayed
     *  cleartext. Every later call started in the clear, including the login
     *  POST carrying the password, before the redirect upgraded it.
     *
     *  Re-persisting the final URL means a redirect to TLS sticks. Only an
     *  upgrade is honoured: a hostile redirect from https down to http is
     *  ignored, so this cannot be used to strip TLS from a URL the user
     *  explicitly typed as https.
     */
    suspend fun checkServer(url: String): Boolean {
        // The URL has to be written before the probe — BaseUrlInterceptor reads
        // it to route the request — but it must NOT survive a failed probe.
        //
        // It used to. hasServer is just "the string is non-empty", so a typo'd
        // address (near-certain when every character costs several D-pad
        // presses) was saved anyway, and the next foreground routed the user to
        // the LOGIN screen rather than back here. They then typed a username and
        // password against a server that does not exist. Restoring the previous
        // value keeps first-run recoverable.
        val previous = prefs.serverUrl.first()
        prefs.setServerUrl(url)
        val ok = try {
            val resp = api.healthCheck()
            if (resp.isSuccessful) {
                adoptRedirectedOrigin(prefs.serverUrl.first(), resp.raw().request.url)
            }
            resp.isSuccessful
        } catch (_: Exception) {
            false
        }
        if (!ok) {
            prefs.setServerUrl(previous ?: "")
        }
        return ok
    }

    /** Persist [finalUrl]'s origin when the health check was upgraded to TLS. */
    private suspend fun adoptRedirectedOrigin(requested: String?, finalUrl: HttpUrl) {
        val asked = requested?.toHttpUrlOrNull() ?: return
        if (asked.isHttps || !finalUrl.isHttps) return
        if (!asked.host.equals(finalUrl.host, ignoreCase = true)) return
        prefs.setServerUrl("https://" + finalUrl.host + portSuffix(finalUrl))
    }

    private fun portSuffix(u: HttpUrl): String =
        if (u.port == HttpUrl.defaultPort(u.scheme)) "" else ":" + u.port

    /** Upgrade a stale cleartext origin to TLS before an unauthenticated POST.
     *
     *  Pairing and login both open with a POST. When the stored origin is
     *  http but the server actually answers on TLS, the edge 301s and OkHttp
     *  follows it by RFC-downgrading the POST to a GET — the API replies 405,
     *  and the caller's retry loop spins forever. On the pairing screen that
     *  looks like "the code never shows up" (server log: GET /auth/pair/code
     *  405 every 5 s, the retry cadence). checkServer() already adopts the
     *  TLS origin during first-run setup, but an install that persisted http
     *  BEFORE that fix landed — or whose server moved behind TLS later —
     *  never re-runs setup, so the stored origin stays cleartext for life.
     *
     *  The probe is a safe GET (redirect-following can't corrupt it) and
     *  reuses adoptRedirectedOrigin, so only a same-host upgrade to https is
     *  ever persisted. A plain-http LAN server answers without a redirect and
     *  nothing changes. Probe failures are swallowed — the caller's own
     *  request will surface the real reachability error. */
    private suspend fun ensureTlsUpgradedOrigin() {
        val stored = prefs.serverUrl.first()?.toHttpUrlOrNull() ?: return
        if (stored.isHttps) return
        try {
            val resp = api.healthCheck()
            if (resp.isSuccessful) {
                adoptRedirectedOrigin(prefs.serverUrl.first(), resp.raw().request.url)
            }
        } catch (_: Exception) {
            // Unreachable — let the caller's request report it.
        }
    }

    /** Public entry point for the same upgrade, for an ALREADY-signed-in
     *  session (MainActivity runs it once per launch).
     *
     *  The login/pairing probes only cover the sign-in paths. A session that
     *  predates them — or a server that moved behind TLS after setup — keeps
     *  a cleartext origin for life, and every mutating call then dies on the
     *  301's POST→GET rewrite: progress PUTs fail silently, so resume
     *  positions simply stop persisting with no visible error. */
    suspend fun healStaleOrigin() = ensureTlsUpgradedOrigin()

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
            // 202 carries no `data` — see PairPollResponse.
            202 -> PollResult.Pending
            410 -> PollResult.Expired
            else -> PollResult.Failure("HTTP ${resp.code()}")
        }
    }

    /** Persist the pair-issued tokens — symmetric with [login]'s
     *  side effect so the pairing UI lands the user in the same
     *  signed-in state a password login would. */
    suspend fun completePairing(pair: TokenPair) {
        prefs.setTokens(pair.access_token, pair.refresh_token, pair.asset_token)
        prefs.setUser(pair.user_id, pair.username)
        authInterceptor.invalidateCache()
        invalidateIdentityCaches()
    }

    sealed class PollResult {
        data object Pending : PollResult()
        data object Expired : PollResult()
        data class Done(val pair: TokenPair) : PollResult()
        data class Failure(val reason: String) : PollResult()
    }
}
