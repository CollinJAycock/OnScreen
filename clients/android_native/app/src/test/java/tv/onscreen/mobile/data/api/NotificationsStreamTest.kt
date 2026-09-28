package tv.onscreen.mobile.data.api

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import kotlinx.coroutines.flow.catch
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.Interceptor
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.model.asPlaybackStop
import tv.onscreen.mobile.data.model.asProgressUpdate

/**
 * The SSE parse layer: every event type the server multiplexes must parse
 * (a payload-typed `data` field used to reject `playback.stop` outright), and
 * media-request notifications must never get past it. Also guards the
 * termination contract NotificationsRepository's reconnect relies on — a
 * server close completes the flow exceptionally.
 */
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

    /** Rewrites the stream's placeholder host onto MockWebServer, as
     *  BaseUrlInterceptor does in the real graph. */
    private fun redirectToMockServer() = Interceptor { chain ->
        val base = server.url("/")
        val url = chain.request().url.newBuilder()
            .scheme(base.scheme).host(base.host).port(base.port).build()
        chain.proceed(chain.request().newBuilder().url(url).build())
    }

    private fun stream() = NotificationsStream(
        OkHttpClient.Builder().addInterceptor(redirectToMockServer()).build(),
        Moshi.Builder().build(),
    )

    @Test
    fun `playback stop event parses with its payload`() {
        val item = stream().parse(
            """{"id":"","type":"playback.stop","item_id":"m1","read":false,"created_at":1759000000000,""" +
                """"data":{"item_id":"m1","session_id":"s1","decision":"transcode","message":"maintenance"}}""",
        )
        assertThat(item).isNotNull()
        val stop = item!!.asPlaybackStop()!!
        assertThat(stop.item_id).isEqualTo("m1")
        assertThat(stop.session_id).isEqualTo("s1")
        assertThat(stop.message).isEqualTo("maintenance")
    }

    @Test
    fun `progress event still parses`() {
        val item = stream().parse(
            """{"type":"progress.updated","created_at":1,"data":{"item_id":"m1","position_ms":5000,"duration_ms":9000,"state":"playing"}}""",
        )
        assertThat(item!!.asProgressUpdate()!!.position_ms).isEqualTo(5_000L)
    }

    @Test
    fun `plain user notification without data parses`() {
        val item = stream().parse("""{"id":"n1","type":"new_content","title":"New","body":"x","read":false,"created_at":2}""")
        assertThat(item!!.title).isEqualTo("New")
    }

    @Test
    fun `request notifications are dropped`() {
        listOf("request_created", "request_approved", "request_available", "request_season_available").forEach { t ->
            assertThat(stream().parse("""{"id":"n","type":"$t","title":"Request","body":"b","created_at":3}""")).isNull()
        }
    }

    @Test
    fun `malformed rows are dropped`() {
        assertThat(stream().parse("not json")).isNull()
        assertThat(stream().parse("""{"title":"no type"}""")).isNull()
    }

    @Test
    fun `live stream never emits request rows and closes exceptionally`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "text/event-stream")
                .setBody(
                    ": keepalive\n\n" +
                        "data: {\"type\":\"request_approved\",\"title\":\"Approved\",\"created_at\":1}\n\n" +
                        "data: {\"type\":\"playback.stop\",\"created_at\":2,\"data\":{\"item_id\":\"m1\"}}\n\n",
                ),
        )
        var failure: Throwable? = null
        val items = withTimeout(10_000) {
            stream().subscribe().catch { failure = it }.toList()
        }
        assertThat(items.map { it.type }).containsExactly("playback.stop")
        assertThat(failure).isInstanceOf(java.io.IOException::class.java)
    }

    @Test
    fun `an HTTP refusal closes with its status and never reports open`() = runBlocking {
        server.enqueue(
            MockResponse().setResponseCode(429)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"error":{"code":"TOO_MANY_SSE","message":"too many"}}"""),
        )
        var opened = false
        var failure: Throwable? = null
        withTimeout(10_000) {
            stream().subscribe(onOpen = { opened = true }).catch { failure = it }.toList()
        }
        assertThat(opened).isFalse()
        assertThat((failure as SseHttpException).code).isEqualTo(429)
    }

    @Test
    fun `an accepted stream reports open before it ends`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "text/event-stream")
                .setBody(": keepalive\n\n"),
        )
        var opened = false
        var failure: Throwable? = null
        withTimeout(10_000) {
            stream().subscribe(onOpen = { opened = true }).catch { failure = it }.toList()
        }
        assertThat(opened).isTrue()
        // A server close is a drop, not a refusal.
        assertThat(failure).isInstanceOf(java.io.IOException::class.java)
        assertThat(failure).isNotInstanceOf(SseHttpException::class.java)
    }
}
