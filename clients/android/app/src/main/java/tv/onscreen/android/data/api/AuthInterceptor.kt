package tv.onscreen.android.data.api

import android.net.Uri
import kotlinx.coroutines.runBlocking
import okhttp3.Interceptor
import okhttp3.Response
import tv.onscreen.android.data.prefs.ServerPrefs

/**
 * Attaches Authorization: Bearer header to API requests. Used for
 * both Retrofit JSON traffic and image fetches via Coil (the singleton
 * ImageLoader configured in OnScreenApp uses this same OkHttp client).
 *
 * Skip list is for paths that intentionally bypass our auth layer:
 *
 * - auth/login, auth/refresh — would fight an expired Bearer with
 *   itself; auth flow handles auth on its own.
 * - health/live — probed before login during server-URL setup, no
 *   token exists yet.
 * - /media/stream/ — Media3 ExoPlayer uses its own HTTP stack, not
 *   OkHttp; the route is reached via transcode session URLs that
 *   carry per-session ?token=, not via this interceptor.
 *
 * Notably NOT skipped: /artwork/. Coil's ImageLoader does run through
 * OkHttp and the server's RequiredAllowQueryToken middleware accepts
 * the Bearer header. Adding artwork to the skip list (as we did
 * originally) was the bug behind "no art on Fire TV / cross-origin
 * builds": every image request hit the server unauthenticated → 401 →
 * Coil rendered the placeholder colour for every card.
 */
class AuthInterceptor(private val prefs: ServerPrefs) : Interceptor {

    private val skipPaths = listOf(
        "auth/login",
        "auth/refresh",
        "health/live",
        "/media/stream/",
    )

    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        val path = request.url.encodedPath

        // Scope auth to our own server. The shared OkHttp/Coil stack
        // also fetches external images (TMDB poster CDN for the
        // "Request more" search row), and TMDB rejects requests that
        // carry an unknown bearer with 401 — Coil swallows that and
        // shows the placeholder. Match by host: anything that isn't
        // the configured server passes through unmodified.
        val serverHost = runBlocking { prefs.getServerUrl() }?.let {
            runCatching { Uri.parse(it).host }.getOrNull()
        }
        if (serverHost != null && !request.url.host.equals(serverHost, ignoreCase = true)) {
            return chain.proceed(request)
        }

        if (skipPaths.any { path.contains(it) }) {
            return chain.proceed(request)
        }

        val token = runBlocking { prefs.getAccessToken() }
        if (token.isNullOrEmpty()) {
            return chain.proceed(request)
        }

        val authed = request.newBuilder()
            .header("Authorization", "Bearer $token")
            .build()
        return chain.proceed(authed)
    }
}
