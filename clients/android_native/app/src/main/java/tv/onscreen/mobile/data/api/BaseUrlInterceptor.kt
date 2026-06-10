package tv.onscreen.mobile.data.api

import kotlinx.coroutines.runBlocking
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Interceptor
import okhttp3.Response
import tv.onscreen.mobile.data.prefs.ServerPrefs

/**
 * Rewrites the placeholder base URL (http://localhost/) to the actual
 * server URL stored in preferences. This allows Retrofit to be created
 * at DI time before the user configures a server.
 */
class BaseUrlInterceptor(private val prefs: ServerPrefs) : Interceptor {

    override fun intercept(chain: Interceptor.Chain): Response {
        val original = chain.request()

        // Only rewrite the placeholder host Retrofit was built with. Absolute
        // URLs (e.g. the external TMDB poster CDN that the Discover/"Request"
        // row loads through the shared Coil client) must pass through untouched
        // — otherwise their host gets rewritten to the configured server, the
        // image 404s, and AuthInterceptor (which runs after this) attaches the
        // Bearer to that mis-routed external request.
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

    private companion object {
        // The placeholder host Retrofit's baseUrl ("http://localhost/") uses.
        const val PLACEHOLDER_HOST = "localhost"
    }
}
