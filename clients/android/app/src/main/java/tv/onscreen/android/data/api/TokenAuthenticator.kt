package tv.onscreen.android.data.api

import kotlinx.coroutines.runBlocking
import okhttp3.Authenticator
import okhttp3.Request
import okhttp3.Response
import okhttp3.Route
import tv.onscreen.android.data.model.RefreshRequest
import tv.onscreen.android.data.prefs.ServerPrefs

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
                    prefs.setTokens(pair.access_token, pair.refresh_token)
                    prefs.setUser(pair.user_id, pair.username)
                }

                response.request.newBuilder()
                    .header("Authorization", "Bearer ${pair.access_token}")
                    .build()
            } catch (e: Exception) {
                runBlocking { prefs.clearAuth() }
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
