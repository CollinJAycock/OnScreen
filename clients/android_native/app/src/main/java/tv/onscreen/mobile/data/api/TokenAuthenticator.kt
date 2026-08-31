package tv.onscreen.mobile.data.api

import kotlinx.coroutines.runBlocking
import okhttp3.Authenticator
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Request
import okhttp3.Response
import okhttp3.Route
import retrofit2.HttpException
import tv.onscreen.mobile.data.downloads.DownloadStore
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
    // Involuntary sign-out has to erase offline media too — see
    // clearAuthAndCache. Lazy provider rather than the store itself: this
    // class is constructed inside the OkHttp graph, and DownloadStore pulls
    // in Moshi, which would close a Hilt dependency cycle.
    private val downloadStoreProvider: () -> DownloadStore,
) : Authenticator {

    private val lock = Any()

    /** Clear auth AND the bearer cache, so the next request re-reads.
     *
     *  This is the INVOLUNTARY sign-out path (no refresh token, or a
     *  definitively-rejected refresh) — the one a remote revoke after a phone
     *  is lost, sold, or handed on actually travels down. It must erase
     *  downloads for the same reason the voluntary path in SettingsViewModel
     *  does: DownloadEntry carries no user or server field, and offline
     *  playback deliberately falls back to the local file whenever the item
     *  fetch throws — which a content-rating 403 does, indistinguishably from
     *  a network blip. Leaving the media meant the next person to pair, a
     *  second household member or a restricted child profile, could open
     *  Downloads and play everything the previous account had. */
    private suspend fun clearAuthAndCache() {
        prefs.clearAuth()
        authInterceptor.invalidateCache()
        runCatching { downloadStoreProvider().clearAll() }
    }

    override fun authenticate(route: Route?, response: Response): Request? {
        // Scope auth to our own server — the same guard AuthInterceptor
        // applies, and it must be repeated here. This client is also Coil's
        // image backend and fetches third-party URLs (TMDB Discover posters
        // arrive as absolute foreign URLs in DiscoverItem.poster_url, and
        // BaseUrlInterceptor passes those through untouched). AuthInterceptor
        // correctly withholds the Bearer from them, but OkHttp invokes the
        // client Authenticator on a 401 from ANY host, and the follow-up
        // request it returns is dispatched from inside
        // RetryAndFollowUpInterceptor — BELOW the application interceptors —
        // so AuthInterceptor never gets a second chance to strip what we add
        // here. Without this check, any non-server host that answers 401
        // receives the user's full-scope OnScreen access token, delivered to
        // an internet host the attacker controls and logs.
        // Parsed with OkHttp's HttpUrl rather than android.net.Uri: this is a
        // security boundary and must be exercisable in JVM unit tests, where
        // android.jar is stubbed and Uri.parse() returns null — which would
        // silently disable the check under test while it still worked in
        // production, i.e. exactly the kind of guard that rots unnoticed.
        // Host AND port: on a home LAN the media server shares a host with
        // whatever else the box runs (`10.0.0.5:7070` OnScreen, `:8096`
        // Jellyfin, `:32400` Plex), so a host-only match would hand the token
        // to a neighbouring service that answered 401.
        // Ported from clients/android/.../TokenAuthenticator.kt — keep in step.
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
