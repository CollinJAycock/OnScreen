package tv.onscreen.android.data.api

import kotlinx.coroutines.runBlocking
import okhttp3.Interceptor
import okhttp3.Response
import tv.onscreen.android.data.prefs.ServerPrefs

/**
 * Attaches Authorization: Bearer header to API requests.
 * Skips auth/login, auth/refresh, health, artwork, and media stream paths.
 */
class AuthInterceptor(private val prefs: ServerPrefs) : Interceptor {

    private val skipPaths = listOf(
        "auth/login",
        "auth/refresh",
        "health/live",
        "/artwork/",
        "/media/stream/",
    )

    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        val path = request.url.encodedPath

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
