package tv.onscreen.mobile.data.repository

import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.LoginRequest
import tv.onscreen.mobile.data.model.LogoutRequest
import tv.onscreen.mobile.data.model.PairCodeResponse
import tv.onscreen.mobile.data.model.TokenPair
import tv.onscreen.mobile.data.prefs.ServerPrefs
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class AuthRepository @Inject constructor(
    private val api: OnScreenApi,
    private val prefs: ServerPrefs,
) {
    suspend fun login(username: String, password: String): TokenPair {
        val pair = api.login(LoginRequest(username, password)).data
        prefs.setTokens(pair.access_token, pair.refresh_token)
        prefs.setUser(pair.user_id, pair.username)
        return pair
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
        prefs.clearAuth()
    }

    /** Check server reachability by hitting the health endpoint. */
    suspend fun checkServer(url: String): Boolean {
        prefs.setServerUrl(url)
        return try {
            api.healthCheck().isSuccessful
        } catch (_: Exception) {
            false
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
