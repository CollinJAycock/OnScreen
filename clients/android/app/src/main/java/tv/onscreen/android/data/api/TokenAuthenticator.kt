package tv.onscreen.android.data.api

import kotlinx.coroutines.runBlocking
import okhttp3.Authenticator
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
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
        // Scope auth to our own server — the same guard AuthInterceptor
        // applies, and it must be repeated here. This client is also Coil's
        // image backend and fetches third-party URLs (TMDB Discover posters,
        // M3U channel logos). AuthInterceptor correctly withholds the Bearer
        // from those, but OkHttp invokes the client Authenticator on a 401
        // from ANY host, and the follow-up request it returns is dispatched
        // from inside RetryAndFollowUpInterceptor — BELOW the application
        // interceptors — so AuthInterceptor never gets a second chance to
        // strip what we add here. Without this check, any non-server host
        // that answers 401 receives the user's OnScreen access token.
        // Parsed with OkHttp's HttpUrl rather than android.net.Uri: this is a
        // security boundary and must be exercisable in JVM unit tests, where
        // android.jar is stubbed and Uri.parse() returns null — which would
        // silently disable the check under test while it still worked in
        // production, i.e. exactly the kind of guard that rots unnoticed.
        // Host AND port: on a home LAN the media server shares a host with
        // whatever else the box runs (`10.0.0.5:7070` OnScreen, `:8096`
        // Jellyfin, `:32400` Plex), so a host-only match would hand the token
        // to a neighbouring service that answered 401. Every OkHttp request
        // the app makes to its own API goes through BaseUrlInterceptor, which
        // stamps the configured scheme/host/port verbatim, so an exact origin
        // match never rejects legitimate traffic.
        val serverUrl = runBlocking { prefs.getServerUrl()?.toHttpUrlOrNull() }
        if (serverUrl != null) {
            val target = response.request.url
            if (!target.host.equals(serverUrl.host, ignoreCase = true) ||
                target.port != serverUrl.port
            ) {
                return null
            }
        }

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
