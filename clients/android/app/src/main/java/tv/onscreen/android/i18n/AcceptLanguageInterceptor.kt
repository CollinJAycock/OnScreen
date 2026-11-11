package tv.onscreen.android.i18n

import okhttp3.Interceptor
import okhttp3.Response

/**
 * Sends the app's language ("en", "es" or "zh-Hans", from [language]) as
 * `Accept-Language` on every request, so the server writes the messages a
 * person sees (errors the app shows as e.message) in the language on screen.
 * Same header the web and TV web apps send (docs/i18n.md, "The server's own
 * text"). Read per request, so a language change applies to the next call.
 */
class AcceptLanguageInterceptor(private val language: () -> String) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val tag = runCatching(language).getOrNull()?.takeIf { it.isNotBlank() } ?: AppLanguage.ENGLISH
        return chain.proceed(
            chain.request().newBuilder()
                .header(HEADER, tag)
                .build(),
        )
    }

    companion object {
        const val HEADER = "Accept-Language"
    }
}
