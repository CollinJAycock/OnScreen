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
 * reconnects via `retryWhen`, which fires ONLY on an exception. If the
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
    fun `media-request notifications are never emitted`() = runBlocking {
        // Requests are not a feature of the Android apps: every request_*
        // type is dropped at the parse layer, before any subscriber, while
        // the other types keep flowing in order.
        server.enqueue(
            sse(
                "data: {\"id\":\"r1\",\"type\":\"request_pending\",\"title\":\"New request\"}\n\n" +
                    "data: {\"id\":\"r2\",\"type\":\"request_available\",\"title\":\"Available\"}\n\n" +
                    "data: {\"id\":\"n1\",\"type\":\"playback.stop\",\"data\":{\"item_id\":\"i1\"}}\n\n" +
                    "data: {\"id\":\"r3\",\"type\":\"request_season_available\",\"title\":\"S2\"}\n\n" +
                    "data: {\"id\":\"n2\",\"type\":\"issue_resolved\",\"title\":\"Fixed\"}\n\n",
            ),
        )

        val seen = mutableListOf<String>()
        try {
            withTimeout(10_000) { stream().subscribe().collect { seen.add(it.id) } }
        } catch (_: Throwable) {
            // The server closing the body ends the flow exceptionally (by design).
        }

        assertThat(seen).containsExactly("n1", "n2").inOrder()
    }

    @Test
    fun `hidden notification types are exactly the request family`() {
        for (t in listOf(
            "request_created", "request_pending", "request_approved", "request_declined",
            "request_available", "request_failed", "request_season_available", "request.anything",
        )) {
            assertThat(tv.onscreen.android.data.model.isHiddenNotificationType(t)).isTrue()
        }
        for (t in listOf(
            "progress.updated", "playback.transfer", "playback.stop", "new_content",
            "scan_complete", "system", "issue_reported", "issue_resolved", "requested",
        )) {
            assertThat(tv.onscreen.android.data.model.isHiddenNotificationType(t)).isFalse()
        }
    }

    @Test
    fun `the SSE client reads with a timeout above the keepalive and no call timeout`() {
        // The server writes a ': keepalive' comment every 30 s. A read timeout
        // of three intervals never trips on a healthy stream and turns a
        // half-open socket into a reconnect. (It was 0 — unbounded — with an
        // event-level watchdog that could not see the comments and closed
        // every healthy idle stream after 150-180 s.) The shared client's
        // whole-call ceiling would kill the stream outright, so it's cleared.
        val shared = OkHttpClient.Builder()
            .readTimeout(60, java.util.concurrent.TimeUnit.SECONDS)
            .callTimeout(120, java.util.concurrent.TimeUnit.SECONDS)
            .build()
        val s = NotificationsStream(shared, Moshi.Builder().build())

        val sseClient = NotificationsStream::class.java
            .getDeclaredField("sseClient")
            .apply { isAccessible = true }
            .get(s) as OkHttpClient

        assertThat(sseClient.readTimeoutMillis).isEqualTo(90_000)
        assertThat(sseClient.callTimeoutMillis).isEqualTo(0)
        // Connect timeout is left as the shared client has it.
        assertThat(sseClient.connectTimeoutMillis).isEqualTo(shared.connectTimeoutMillis)
    }

    @Test
    fun `comment keepalives keep a quiet stream open`() = runBlocking {
        // Only comments for a while, then an event: the stream must still be
        // the same open call when the event arrives (comments reach the socket
        // read, not the listener).
        server.enqueue(
            sse(": keepalive\n\n: keepalive\n\n: keepalive\n\n" +
                "data: {\"id\":\"n1\",\"type\":\"playback.transfer\",\"title\":\"t\"}\n\n"),
        )

        val first = withTimeout(10_000) { stream().subscribe().first() }

        assertThat(first.id).isEqualTo("n1")
        assertThat(server.requestCount).isEqualTo(1)
    }

    @Test
    fun `an HTTP refusal carries its status for the reconnect policy`() = runBlocking {
        server.enqueue(MockResponse().setResponseCode(429))

        var failure: Throwable? = null
        try {
            withTimeout(10_000) { stream().subscribe().toList() }
        } catch (t: Throwable) {
            failure = t
        }

        assertThat(failure).isInstanceOf(SseHttpException::class.java)
        assertThat((failure as SseHttpException).code).isEqualTo(429)
    }

    @Test
    fun `onOpen runs once the server accepts the stream`() = runBlocking {
        server.enqueue(sse("data: {\"id\":\"n1\",\"type\":\"progress.updated\",\"title\":\"t\"}\n\n"))
        var opened = false

        withTimeout(10_000) { stream().subscribe { opened = true }.first() }

        assertThat(opened).isTrue()
    }
}
