package tv.onscreen.android.data.api

import kotlinx.coroutines.runBlocking
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Interceptor
import okhttp3.Response
import tv.onscreen.android.data.prefs.ServerPrefs

/**
 * Rewrites the placeholder base URL (http://localhost/) to the actual
 * server URL stored in preferences. This allows Retrofit to be created
 * at DI time before the user configures a server.
 *
 * Only requests still aimed at the placeholder host are rewritten.
 * The same OkHttp client is Coil's HTTP backend, and the Discover row
 * loads absolute TMDB CDN poster URLs — rewriting those would mis-route
 * them to the media server (404 → placeholder tile on every external
 * poster). Anything with a real host passes through untouched.
 */
class BaseUrlInterceptor(private val prefs: ServerPrefs) : Interceptor {

    override fun intercept(chain: Interceptor.Chain): Response {
        val original = chain.request()

        if (!original.url.host.equals(PLACEHOLDER_HOST, ignoreCase = true)) {
            return chain.proceed(original)
        }

        val serverUrl = runBlocking { prefs.getServerUrl() }

        if (serverUrl.isNullOrEmpty()) {
            return chain.proceed(original)
        }

        val baseUrl = serverUrl.toHttpUrlOrNull() ?: return chain.proceed(original)

        val newUrl = original.url.newBuilder()
            .scheme(baseUrl.scheme)
            .host(baseUrl.host)
            .port(baseUrl.port)
            .build()

        val newRequest = original.newBuilder().url(newUrl).build()
        return chain.proceed(newRequest)
    }

    companion object {
        /** Placeholder base URL Retrofit is built with at DI time — see NetworkModule. */
        const val PLACEHOLDER_BASE_URL = "http://localhost/"
        private const val PLACEHOLDER_HOST = "localhost"
    }
}
