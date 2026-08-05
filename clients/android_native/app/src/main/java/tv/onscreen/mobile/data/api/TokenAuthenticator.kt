package tv.onscreen.mobile.data.api

import kotlinx.coroutines.runBlocking
import okhttp3.Authenticator
import okhttp3.Request
import okhttp3.Response
import okhttp3.Route
import retrofit2.HttpException
import tv.onscreen.mobile.data.model.RefreshRequest
import tv.onscreen.mobile.data.prefs.ServerPrefs

/**
 * Handles 401 responses by refreshing the access token. If the refresh
 * also fails, clears auth state so the UI can redirect to login.
 *
 * Synchronized to prevent concurrent refreshes from racing.
 */
class TokenAuthenticator(
    private val prefs: ServerPrefs,
    private val apiProvider: () -> OnScreenApi,
) : Authenticator {

    private val lock = Any()

    override fun authenticate(route: Route?, response: Response): Request? {
        // Don't retry refresh or login failures.
        val path = response.request.url.encodedPath
        if (path.contains("auth/refresh") || path.contains("auth/login")) {
            return null
        }

        // Avoid infinite retry loops.
        if (responseCount(response) >= 2) {
            return null
        }

        synchronized(lock) {
            // Another thread may have already refreshed — check if the token changed.
            val currentToken = runBlocking { prefs.getAccessToken() }
            val usedToken = response.request.header("Authorization")?.removePrefix("Bearer ")

            if (currentToken != null && currentToken != usedToken) {
                // Token was refreshed by another thread — retry with the new one.
                return response.request.newBuilder()
                    .header("Authorization", "Bearer $currentToken")
                    .build()
            }

            // Attempt refresh.
            val refreshToken = runBlocking { prefs.getRefreshToken() } ?: run {
                runBlocking { prefs.clearAuth() }
                return null
            }

            return try {
                val pair = runBlocking {
                    apiProvider().refresh(RefreshRequest(refreshToken))
                }.data

                runBlocking {
                    prefs.setTokens(pair.access_token, pair.refresh_token, pair.asset_token)
                    prefs.setUser(pair.user_id, pair.username)
                }

                response.request.newBuilder()
                    .header("Authorization", "Bearer ${pair.access_token}")
                    .build()
            } catch (e: Exception) {
                // Only a DEFINITIVE rejection invalidates the session. A
                // timeout, a dropped Wi-Fi frame, or a 502 while the server
                // restarts is transient — wiping tokens there logged the user
                // out mid-playback and forced a full re-pair, from a blip the
                // next request would have survived. 401/403 from the refresh
                // endpoint means the refresh token really is dead (expired,
                // revoked, or reuse-detected), and only then do we clear.
                val definitive = (e as? HttpException)?.code() in setOf(401, 403)
                if (definitive) runBlocking { prefs.clearAuth() }
                null
            }
        }
    }

    private fun responseCount(response: Response): Int {
        var count = 1
        var prior = response.priorResponse
        while (prior != null) {
            count++
            prior = prior.priorResponse
        }
        return count
    }
}
