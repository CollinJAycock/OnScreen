package tv.onscreen.mobile.di

import com.google.common.truth.Truth.assertThat
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Before
import org.junit.Test
import java.io.IOException
import java.util.concurrent.TimeUnit

/**
 * The shared client's whole-call ceiling also bounds the body read, which cut
 * every offline download off after two minutes. Whole-file readers use
 * [withoutCallTimeout]; this pins that the derived client lets a long body
 * finish while keeping the read timeout.
 */
class WithoutCallTimeoutTest {

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

    /** A body that takes ~1 s to arrive, in 1 KB slices every 100 ms. */
    private fun slowBody(): MockResponse =
        MockResponse()
            .setResponseCode(200)
            .setBody("x".repeat(10 * 1024))
            .throttleBody(1024, 100, TimeUnit.MILLISECONDS)

    private val shared: OkHttpClient = OkHttpClient.Builder()
        .readTimeout(5, TimeUnit.SECONDS)
        .callTimeout(300, TimeUnit.MILLISECONDS)
        .build()

    private fun readAll(client: OkHttpClient): Int =
        client.newCall(Request.Builder().url(server.url("/media/download/f")).build()).execute().use {
            it.body!!.bytes().size
        }

    @Test
    fun `the shared ceiling cuts a long body off`() {
        server.enqueue(slowBody())
        val failure = runCatching { readAll(shared) }.exceptionOrNull()
        assertThat(failure).isInstanceOf(IOException::class.java)
    }

    @Test
    fun `the derived client reads the whole body`() {
        server.enqueue(slowBody())
        assertThat(readAll(shared.withoutCallTimeout())).isEqualTo(10 * 1024)
    }

    @Test
    fun `only the call ceiling is dropped`() {
        val derived = shared.withoutCallTimeout()
        assertThat(derived.callTimeoutMillis).isEqualTo(0)
        assertThat(derived.readTimeoutMillis).isEqualTo(shared.readTimeoutMillis)
        assertThat(derived.connectionPool).isSameInstanceAs(shared.connectionPool)
        assertThat(derived.interceptors).isEqualTo(shared.interceptors)
        assertThat(derived.authenticator).isSameInstanceAs(shared.authenticator)
    }
}
