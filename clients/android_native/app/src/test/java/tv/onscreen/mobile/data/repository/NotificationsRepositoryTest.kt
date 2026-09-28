package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.currentTime
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.Test
import tv.onscreen.mobile.data.api.NotificationsStream
import tv.onscreen.mobile.data.api.SseHttpException
import java.io.IOException
import kotlin.random.Random
import tv.onscreen.mobile.data.model.NotificationItem
import tv.onscreen.mobile.data.model.PlaybackStopEvent
import tv.onscreen.mobile.data.prefs.ServerPrefs

/** The shared SSE fan-out: typed views filter by event type, the socket
 *  follows the signed-in identity, and re-dials back off. */
@OptIn(ExperimentalCoroutinesApi::class)
class NotificationsRepositoryTest {

    private val progress = NotificationItem(
        type = "progress.updated",
        data = mapOf("item_id" to "m1", "position_ms" to 1000.0, "duration_ms" to 9000.0, "state" to "playing"),
    )
    private val stop = NotificationItem(
        type = "playback.stop",
        item_id = "m1",
        data = mapOf("item_id" to "m1", "message" to "bye"),
    )
    private val other = NotificationItem(type = "new_content", title = "New")

    private fun prefs(
        url: String? = "http://srv",
        user: MutableStateFlow<String?> = MutableStateFlow("u1"),
        loggedIn: Boolean = true,
    ): ServerPrefs = mockk<ServerPrefs>().also {
        every { it.serverUrl } returns flowOf(url)
        every { it.userId } returns user
        every { it.isLoggedIn } returns flowOf(loggedIn)
    }

    /** A stream that emits [items] then stays open, like a live SSE. */
    private fun openStream(vararg items: NotificationItem) = flow {
        items.forEach { emit(it) }
        awaitCancellation()
    }

    @Test
    fun `subscribePlaybackStops emits only stop events`() = runBlocking {
        val stream = mockk<NotificationsStream>()
        every { stream.subscribe(any()) } returns openStream(other, progress, stop)
        val repo = NotificationsRepository(stream, prefs())

        val ev = withTimeout(5_000) { repo.subscribePlaybackStops().first() }
        assertThat(ev).isEqualTo(PlaybackStopEvent(item_id = "m1", message = "bye"))
    }

    @Test
    fun `subscribeProgressUpdates emits only progress events`() = runBlocking {
        val stream = mockk<NotificationsStream>()
        every { stream.subscribe(any()) } returns openStream(stop, other, progress)
        val repo = NotificationsRepository(stream, prefs())

        val ev = withTimeout(5_000) { repo.subscribeProgressUpdates().first() }
        assertThat(ev.item_id).isEqualTo("m1")
        assertThat(ev.position_ms).isEqualTo(1_000L)
    }

    @Test
    fun `signed out never opens the stream`() = runBlocking {
        val stream = mockk<NotificationsStream>()
        every { stream.subscribe(any()) } returns openStream(stop)
        val repo = NotificationsRepository(stream, prefs(loggedIn = false))

        val ev = withTimeoutOrNull(500) { repo.subscribePlaybackStops().first() }
        assertThat(ev).isNull()
        verify(exactly = 0) { stream.subscribe(any()) }
    }

    // Collectors run on Default: mockk's verify(timeout) blocks the
    // runBlocking thread while it waits.
    @Test
    fun `two subscribers share one connection`() = runBlocking {
        val stream = mockk<NotificationsStream>()
        every { stream.subscribe(any()) } returns openStream()
        val repo = NotificationsRepository(stream, prefs())

        val a = launch(Dispatchers.Default) { repo.subscribePlaybackStops().collect { } }
        val b = launch(Dispatchers.Default) { repo.subscribeProgressUpdates().collect { } }
        verify(timeout = 5_000, exactly = 1) { stream.subscribe(any()) }
        a.cancel()
        b.cancel()
        verify(exactly = 1) { stream.subscribe(any()) }
    }

    @Test
    fun `an account switch re-dials the stream`() = runBlocking {
        val stream = mockk<NotificationsStream>()
        every { stream.subscribe(any()) } returns openStream()
        val user = MutableStateFlow<String?>("alice")
        val repo = NotificationsRepository(stream, prefs(user = user))

        val job = launch(Dispatchers.Default) { repo.subscribePlaybackStops().collect { } }
        verify(timeout = 5_000, exactly = 1) { stream.subscribe(any()) }
        user.value = "bob"
        verify(timeout = 5_000, exactly = 2) { stream.subscribe(any()) }
        job.cancel()
    }

    // ── Reconnect backoff ─────────────────────────────────────────────────

    /** Fake dialer: records the virtual time of every dial; [script] decides
     *  whether dial n (0-based) opens first and how it then ends. */
    private class Dialer(val script: (n: Int) -> Pair<Boolean, Throwable>) {
        val dials = mutableListOf<Long>()
        fun open(now: () -> Long, onOpen: () -> Unit) = flow<NotificationItem> {
            val n = dials.size
            dials += now()
            val (opens, end) = script(n)
            if (opens) onOpen()
            throw end
        }
    }

    @Test
    fun `network failures back off exponentially to a cap instead of every 5 s`() = runTest {
        val dialer = Dialer { false to IOException("unreachable") }
        val job = launch { reconnecting { onOpen -> dialer.open({ currentTime }, onOpen) }.collect { } }
        advanceTimeBy(60 * 60_000L) // an hour away from the home LAN
        job.cancel()

        val gaps = dialer.dials.zipWithNext { a, b -> b - a }
        // Each wait sits in [ceiling/2, ceiling] of 5 s, 10 s, 20 s, … 5 min.
        gaps.forEachIndexed { i, gap ->
            val ceiling = minOf(5_000L shl minOf(i, 16), SseReconnectPolicy.MAX_DELAY_MS)
            assertThat(gap).isAtLeast(ceiling / 2)
            assertThat(gap).isAtMost(ceiling)
        }
        // The flat 5 s policy dialed 720 times in that hour.
        assertThat(dialer.dials.size).isLessThan(40)
    }

    @Test
    fun `a successful open resets the backoff`() = runTest {
        // Dials 0-5 fail outright, dial 6 opens then drops, then fail again.
        val dialer = Dialer { n -> (n == 6) to IOException("drop") }
        val job = launch { reconnecting { onOpen -> dialer.open({ currentTime }, onOpen) }.collect { } }
        advanceTimeBy(30 * 60_000L)
        job.cancel()

        val gaps = dialer.dials.zipWithNext { a, b -> b - a }
        assertThat(gaps[5]).isAtLeast(80_000L) // ceiling 160 s before the open
        assertThat(gaps[6]).isAtMost(5_000L) // back to the 5 s ceiling after it
    }

    @Test
    fun `429 TOO_MANY_SSE waits at least half a minute before asking again`() = runTest {
        val dialer = Dialer { false to SseHttpException(429) }
        val job = launch { reconnecting { onOpen -> dialer.open({ currentTime }, onOpen) }.collect { } }
        advanceTimeBy(10 * 60_000L)
        job.cancel()

        val gaps = dialer.dials.zipWithNext { a, b -> b - a }
        assertThat(gaps).isNotEmpty()
        assertThat(gaps.first()).isAtLeast(SseReconnectPolicy.CLIENT_ERROR_DELAY_MS / 2)
        assertThat(dialer.dials.size).isAtMost(6)
    }

    @Test
    fun `401 the authenticator could not refresh stops re-dialing and completes quietly`() = runTest {
        val dialer = Dialer { false to SseHttpException(401) }
        val items = reconnecting { onOpen -> dialer.open({ currentTime }, onOpen) }.toList()
        assertThat(items).isEmpty()
        assertThat(dialer.dials).hasSize(1)
    }

    @Test
    fun `policy - jitter stays inside the ceiling and 4xx starts at a minute`() {
        val random = Random(42)
        repeat(200) {
            val d = SseReconnectPolicy.delayMs(IOException(), failures = 0, random = random)!!
            assertThat(d).isAtLeast(2_500L)
            assertThat(d).isAtMost(5_000L)
        }
        assertThat(SseReconnectPolicy.delayMs(IOException(), failures = 50, random = random))
            .isAtMost(SseReconnectPolicy.MAX_DELAY_MS)
        assertThat(SseReconnectPolicy.delayMs(SseHttpException(403), failures = 0, random = random))
            .isAtLeast(30_000L)
        assertThat(SseReconnectPolicy.delayMs(SseHttpException(503), failures = 0, random = random))
            .isAtMost(5_000L)
        assertThat(SseReconnectPolicy.delayMs(SseHttpException(401), failures = 0, random = random)).isNull()
    }
}
