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
    // Every token write here has to drop AuthInterceptor's bearer cache.
    // Its doc claims ServerPrefs does this from setTokens/clearAuth — it
    // does not, and this class writes prefs directly (bypassing
    // AuthRepository, the only caller that invalidates). Without it, for
    // up to the 5 s TTL after each rotation every new request — including
    // bursts of Coil artwork fetches — carries the just-rotated dead
    // bearer, 401s, and serializes back through this authenticator; after
    // a definitive rejection the revoked bearer keeps being sent.
    private val authInterceptor: AuthInterceptor,
) : Authenticator {

    private val lock = Any()

    /** Clear auth AND the bearer cache, so the next request re-reads. */
    private suspend fun clearAuthAndCache() {
        prefs.clearAuth()
        authInterceptor.invalidateCache()
    }

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
                runBlocking { clearAuthAndCache() }
                return null
            }

            return try {
                val pair = runBlocking {
                    apiProvider().refresh(RefreshRequest(refreshToken))
                }.data

                runBlocking {
                    prefs.setTokens(pair.access_token, pair.refresh_token, pair.asset_token)
                    prefs.setUser(pair.user_id, pair.username)
                    authInterceptor.invalidateCache()
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
                if (definitive) runBlocking { clearAuthAndCache() }
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
