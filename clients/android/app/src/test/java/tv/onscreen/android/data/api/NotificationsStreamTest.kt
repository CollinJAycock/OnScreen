package tv.onscreen.android.data.api

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.Interceptor
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Before
import org.junit.Test

/**
 * Guards the SSE termination contract.
 *
 * [NotificationsRepository][tv.onscreen.android.data.repository.NotificationsRepository]
 * reconnects via `.retry { }`, which fires ONLY on an exception. If the
 * callbackFlow completes normally instead, the retry never runs, the
 * `shareIn` upstream is never restarted, and — because shareIn yields a
 * SharedFlow that never completes — the callers' own outer reconnect loops
 * never iterate either. Cross-device progress sync and "play on this TV" then
 * stay dead for the rest of the foreground session.
 *
 * So: every terminal path must complete the flow EXCEPTIONALLY.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class NotificationsStreamTest {

    private lateinit var server: MockWebServer

    @Before
    fun setUp() {
        server = MockWebServer().apply { start() }
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    /** Rewrites the stream's hardcoded placeholder host onto MockWebServer,
     *  mirroring what BaseUrlInterceptor does in the real graph. */
    private fun redirectToMockServer() = Interceptor { chain ->
        val base = server.url("/")
        val url = chain.request().url.newBuilder()
            .scheme(base.scheme).host(base.host).port(base.port).build()
        chain.proceed(chain.request().newBuilder().url(url).build())
    }

    private fun stream(): NotificationsStream = NotificationsStream(
        OkHttpClient.Builder().addInterceptor(redirectToMockServer()).build(),
        Moshi.Builder().build(),
    )

    private fun sse(body: String) = MockResponse()
        .setHeader("Content-Type", "text/event-stream")
        .setBody(body)

    @Test
    fun `a clean server-side close completes the flow exceptionally`() = runBlocking {
        // Server sends one event then ends the response body normally. The
        // old code called close() with no cause here, which read as "the
        // stream finished successfully" and permanently wedged reconnects.
        server.enqueue(
            sse("data: {\"id\":\"n1\",\"type\":\"progress.updated\",\"title\":\"t\"}\n\n"),
        )

        var failure: Throwable? = null
        try {
            withTimeout(10_000) { stream().subscribe().toList() }
        } catch (t: Throwable) {
            failure = t
        }

        assertThat(failure).isNotNull()
    }

    @Test
    fun `a transport failure completes the flow exceptionally`() = runBlocking {
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START))

        var failure: Throwable? = null
        try {
            withTimeout(10_000) { stream().subscribe().toList() }
        } catch (t: Throwable) {
            failure = t
        }

        assertThat(failure).isNotNull()
    }

    @Test
    fun `a non-2xx response completes the flow exceptionally rather than hanging`() = runBlocking {
        // A 401 (expired token) or 502 (proxy hiccup) must surface as an
        // exception so the retry re-dials once auth recovers.
        server.enqueue(MockResponse().setResponseCode(401))

        var failure: Throwable? = null
        try {
            withTimeout(10_000) { stream().subscribe().toList() }
        } catch (t: Throwable) {
            failure = t
        }

        assertThat(failure).isNotNull()
    }

    @Test
    fun `events are parsed and delivered before termination`() = runBlocking {
        server.enqueue(
            sse(
                "data: {\"id\":\"n1\",\"type\":\"progress.updated\",\"title\":\"first\"}\n\n" +
                    "data: {\"id\":\"n2\",\"type\":\"playback.transfer\",\"title\":\"second\"}\n\n",
            ),
        )

        val first = withTimeout(10_000) { stream().subscribe().first() }

        assertThat(first.id).isEqualTo("n1")
        assertThat(first.type).isEqualTo("progress.updated")
    }

    @Test
    fun `a malformed event is skipped without killing the stream`() = runBlocking {
        server.enqueue(
            sse(
                "data: not-json-at-all\n\n" +
                    "data: {\"id\":\"n2\",\"type\":\"progress.updated\",\"title\":\"survivor\"}\n\n",
            ),
        )

        val first = withTimeout(10_000) { stream().subscribe().first() }

        // The bad frame is dropped; the following good one still arrives.
        assertThat(first.id).isEqualTo("n2")
    }

    @Test
    fun `the SSE client disables the read timeout`() {
        // An event stream is idle by design (one ': keepalive' at connect,
        // then nothing until an event). Inheriting the shared client's 60 s
        // read timeout tore the connection down every minute and dropped
        // pushed events during each reconnect gap.
        val shared = OkHttpClient.Builder()
            .readTimeout(60, java.util.concurrent.TimeUnit.SECONDS)
            .build()
        val s = NotificationsStream(shared, Moshi.Builder().build())

        val sseClient = NotificationsStream::class.java
            .getDeclaredField("sseClient")
            .apply { isAccessible = true }
            .get(s) as OkHttpClient

        assertThat(sseClient.readTimeoutMillis).isEqualTo(0)
        // Connect timeout is deliberately left intact — only reads are unbounded.
        assertThat(sseClient.connectTimeoutMillis).isEqualTo(shared.connectTimeoutMillis)
    }
}
