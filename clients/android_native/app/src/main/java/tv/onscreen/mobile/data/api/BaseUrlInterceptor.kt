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
}
