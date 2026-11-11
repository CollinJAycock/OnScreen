package tv.onscreen.mobile.data.api

import okhttp3.Interceptor
import okhttp3.Response

/**
 * Sends the app's language ("en" / "es" / "zh-Hans") as Accept-Language on
 * every request, as the web and TV apps do, so the server writes the errors
 * it returns in the language on screen (docs/i18n.md, "The server's own
 * text"). [tag] is read per request, so a language change applies to the
 * next call. A request that already names a language keeps it.
 */
class AcceptLanguageInterceptor(private val tag: () -> String) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        if (request.header(HEADER) != null) return chain.proceed(request)
        return chain.proceed(request.newBuilder().header(HEADER, tag()).build())
    }

    private companion object {
        const val HEADER = "Accept-Language"
    }
}
