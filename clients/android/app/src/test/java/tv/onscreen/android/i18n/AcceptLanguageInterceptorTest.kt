package tv.onscreen.android.i18n

import com.google.common.truth.Truth.assertThat
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.After
import org.junit.Before
import org.junit.Test

/** The app's language goes out as Accept-Language on every request, read per
 *  request so a language change applies to the next call. */
class AcceptLanguageInterceptorTest {

    private lateinit var server: MockWebServer

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun send(client: OkHttpClient, header: String? = null): String? {
        server.enqueue(MockResponse().setResponseCode(200))
        val request = Request.Builder().url(server.url("/api/v1/hub")).apply {
            if (header != null) header("Accept-Language", header)
        }.build()
        client.newCall(request).execute().close()
        return server.takeRequest().getHeader("Accept-Language")
    }

    @Test
    fun `sends the app's current language, following a change`() {
        var language = "es"
        val client = OkHttpClient.Builder().addInterceptor(AcceptLanguageInterceptor { language }).build()

        assertThat(send(client)).isEqualTo("es")
        language = "zh-Hans"
        assertThat(send(client)).isEqualTo("zh-Hans")
        language = "en"
        assertThat(send(client)).isEqualTo("en")
    }

    @Test
    fun `replaces a header already on the request`() {
        val client = OkHttpClient.Builder().addInterceptor(AcceptLanguageInterceptor { "es" }).build()
        assertThat(send(client, header = "de-DE")).isEqualTo("es")
    }

    @Test
    fun `falls back to English when the language can't be read`() {
        val blank = OkHttpClient.Builder().addInterceptor(AcceptLanguageInterceptor { " " }).build()
        assertThat(send(blank)).isEqualTo("en")
        val failing = OkHttpClient.Builder().addInterceptor(AcceptLanguageInterceptor { error("no context") }).build()
        assertThat(send(failing)).isEqualTo("en")
    }
}
