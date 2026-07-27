package tv.onscreen.android.data.api

import kotlinx.coroutines.runBlocking
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Interceptor
import okhttp3.Response
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.playback.PlaybackHelper

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
 *
 * Cached server-host + token: every image fetch through Coil hits this
 * interceptor on an OkHttp dispatcher thread, and DataStore's reader
 * is single-coroutine-context. Reading it via runBlocking on every
 * request serialised dispatcher threads — symptom was photo viewer
 * stalling after 4-5 quick D-pad presses (hitting OkHttp's default
 * maxRequestsPerHost). The cached values are refreshed lazily after
 * a short TTL so token rotation still picks up within seconds.
 */
class AuthInterceptor(private val prefs: ServerPrefs) : Interceptor {

    private val skipPaths = listOf(
        "auth/login",
        "auth/refresh",
        "health/live",
        "/media/stream/",
    )

    @Volatile private var cachedServerHost: String? = null
    /** -1 when no server URL is configured yet. Paired with the host so the
     *  origin check matches [TokenAuthenticator]'s — see the note there on
     *  why a host-only match is not enough on a shared LAN host. */
    @Volatile private var cachedServerPort: Int = -1
    @Volatile private var cachedToken: String? = null
    @Volatile private var cacheLoadedAtMs: Long = 0L

    override fun intercept(chain: Interceptor.Chain): Response {
        refreshCacheIfStale()

        val request = chain.request()
        val path = request.url.encodedPath

        // Scope auth to our own server. The shared OkHttp/Coil stack
        // also fetches external images (TMDB poster CDN for the
        // "Request more" search row), and TMDB rejects requests that
        // carry an unknown bearer with 401 — Coil swallows that and
        // shows the placeholder. Match by host: anything that isn't
        // the configured server passes through unmodified.
        val serverHost = cachedServerHost
        if (serverHost != null &&
            (!request.url.host.equals(serverHost, ignoreCase = true) ||
                request.url.port != cachedServerPort)
        ) {
            return chain.proceed(request)
        }

        if (skipPaths.any { path.contains(it) }) {
            return chain.proceed(request)
        }

        val token = cachedToken
        if (token.isNullOrEmpty()) {
            return chain.proceed(request)
        }

        val authed = request.newBuilder()
            .header("Authorization", "Bearer $token")
            .header("X-Client-Capabilities", PlaybackHelper.clientCapabilitiesHeader())
            .build()
        return chain.proceed(authed)
    }

    /**
     * Refresh the cached host + token at most once every [CACHE_TTL_MS].
     * The first call (cacheLoadedAtMs == 0) does the load synchronously
     * via runBlocking — has to, the very first request after process
     * start has nothing else to fall back to. Subsequent refreshes are
     * still synchronous but only fire on a slow cadence so runBlocking
     * isn't on the hot path.
     */
    private fun refreshCacheIfStale() {
        val now = System.currentTimeMillis()
        if (now - cacheLoadedAtMs < CACHE_TTL_MS) return
        synchronized(this) {
            if (now - cacheLoadedAtMs < CACHE_TTL_MS) return
            // HttpUrl, not android.net.Uri — the host comparison below is a
            // security boundary, and under JVM unit tests android.jar is
            // stubbed so Uri.parse() returns null, which would quietly
            // neutralise the check under test while it still worked in
            // production. Same parse the rest of the OkHttp stack uses.
            val (parsed, token) = runBlocking {
                prefs.getServerUrl()?.toHttpUrlOrNull() to prefs.getAccessToken()
            }
            cachedServerHost = parsed?.host
            cachedServerPort = parsed?.port ?: -1
            cachedToken = token
            cacheLoadedAtMs = now
        }
    }

    companion object {
        // 5 s is short enough that token rotation lands on the next
        // request after refresh, long enough that bursts of image
        // fetches (4 100 photos in the viewer, library grids on
        // scroll) all hit the cache.
        private const val CACHE_TTL_MS = 5_000L
    }
}
