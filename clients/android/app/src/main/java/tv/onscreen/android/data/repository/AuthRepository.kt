package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.LoginRequest
import tv.onscreen.android.data.model.LogoutRequest
import tv.onscreen.android.data.model.PairCodeResponse
import tv.onscreen.android.data.model.TokenPair
import tv.onscreen.android.data.model.TotpVerifyRequest
import tv.onscreen.android.data.prefs.ServerPrefs
import kotlinx.coroutines.flow.first
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class AuthRepository @Inject constructor(
    private val api: OnScreenApi,
    private val prefs: ServerPrefs,
    // Process-lifetime @Singleton caches that are keyed to nothing. Every
    // identity transition below has to clear them, or the next user on a shared
    // TV inherits the previous one's state. Neither depends on AuthRepository,
    // so there is no Hilt cycle.
    private val preferences: PreferencesRepository,
    private val capabilities: CapabilitiesRepository,
) {
    /** Clear per-identity caches. Called from every sign-in, sign-out and
     *  pairing completion — the four points where "who is signed in" changes. */
    private fun invalidateIdentityCaches() {
        preferences.invalidate()
        capabilities.invalidate()
    }

    suspend fun login(username: String, password: String): TokenPair {
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
        invalidateIdentityCaches()
        prefs.setTokens(pair.access_token, pair.refresh_token, pair.asset_token)
        prefs.setUser(pair.user_id, pair.username)
    }

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
