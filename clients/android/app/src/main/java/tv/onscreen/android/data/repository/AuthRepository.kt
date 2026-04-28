package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.LoginRequest
import tv.onscreen.android.data.model.LogoutRequest
import tv.onscreen.android.data.model.TokenPair
import tv.onscreen.android.data.prefs.ServerPrefs
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
}
