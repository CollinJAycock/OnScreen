package tv.onscreen.mobile.data.api

import com.google.common.truth.Truth.assertThat
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Before
import org.junit.Test

class AcceptLanguageInterceptorTest {

    private lateinit var server: MockWebServer

    @Before fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After fun tearDown() {
        server.shutdown()
    }

    private fun call(client: OkHttpClient, build: Request.Builder.() -> Unit = {}): String? {
        server.enqueue(MockResponse().setResponseCode(204))
        client.newCall(Request.Builder().url(server.url("/api/v1/x")).apply(build).build()).execute().close()
        return server.takeRequest().getHeader("Accept-Language")
    }

    @Test
    fun `every request carries the app's language`() {
        val client = OkHttpClient.Builder().addInterceptor(AcceptLanguageInterceptor { "zh-Hans" }).build()
        assertThat(call(client)).isEqualTo("zh-Hans")
    }

    @Test
    fun `the language is read per request, so a change applies to the next call`() {
        var tag = "en"
        val client = OkHttpClient.Builder().addInterceptor(AcceptLanguageInterceptor { tag }).build()
        assertThat(call(client)).isEqualTo("en")
        tag = "es"
        assertThat(call(client)).isEqualTo("es")
    }

    @Test
    fun `a request that already names a language keeps it`() {
        val client = OkHttpClient.Builder().addInterceptor(AcceptLanguageInterceptor { "es" }).build()
        assertThat(call(client) { header("Accept-Language", "en") }).isEqualTo("en")
    }
}
