package tv.onscreen.android.data.repository

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
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.currentTime
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.Test
import tv.onscreen.android.data.api.NotificationsStream
import tv.onscreen.android.data.api.SseHttpException
import tv.onscreen.android.data.model.NotificationItem
import tv.onscreen.android.data.prefs.ServerPrefs
import java.io.IOException
import kotlin.random.Random

/** The shared SSE fan-out: dialed only while signed in, re-dialed on an
 *  identity change, and re-dials back off. Mirrors the phone client's
 *  NotificationsRepositoryTest. */
@OptIn(ExperimentalCoroutinesApi::class)
class NotificationsRepositoryTest {

    private val transfer = NotificationItem(
        type = "playback.transfer",
        data = mapOf("item_id" to "m1", "position_ms" to 1000.0, "target_client_name" to "Living Room"),
    )

    private fun prefs(loggedIn: MutableStateFlow<Boolean> = MutableStateFlow(true)): ServerPrefs =
        mockk<ServerPrefs>().also { every { it.isLoggedIn } returns loggedIn }

    /** A stream that emits [items] then stays open, like a live SSE. */
    private fun openStream(vararg items: NotificationItem) = flow {
        items.forEach { emit(it) }
        awaitCancellation()
    }

    @Test
    fun `transfers reach a subscriber while signed in`() = runBlocking {
        val stream = mockk<NotificationsStream>()
        every { stream.subscribe(any()) } returns openStream(transfer)
        val repo = NotificationsRepository(stream, prefs())

        val ev = withTimeout(5_000) { repo.subscribePlaybackTransfers().first() }
        assertThat(ev.target_client_name).isEqualTo("Living Room")
    }

    @Test
    fun `signed out never opens the stream`() = runBlocking {
        // MainActivity subscribes from the Login screen too; the old stream
        // dialed there and got a 401 every 5 s.
        val stream = mockk<NotificationsStream>()
        every { stream.subscribe(any()) } returns openStream(transfer)
        val repo = NotificationsRepository(stream, prefs(MutableStateFlow(false)))

        val ev = withTimeoutOrNull(500) { repo.subscribePlaybackTransfers().first() }
        assertThat(ev).isNull()
        verify(exactly = 0) { stream.subscribe(any()) }
    }

    // Collectors run on Default: mockk's verify(timeout) blocks the
    // runBlocking thread while it waits.
    @Test
    fun `signing out closes the stream and signing in dials it again`() = runBlocking {
        val stream = mockk<NotificationsStream>()
        every { stream.subscribe(any()) } returns openStream()
        val loggedIn = MutableStateFlow(true)
        val repo = NotificationsRepository(stream, prefs(loggedIn))

        val job = launch(Dispatchers.Default) { repo.subscribePlaybackTransfers().collect { } }
        verify(timeout = 5_000, exactly = 1) { stream.subscribe(any()) }
        loggedIn.value = false
        // An identity bump while signed out dials nothing.
        repo.restartForIdentityChange()
        Thread.sleep(200)
        verify(exactly = 1) { stream.subscribe(any()) }
        loggedIn.value = true
        verify(timeout = 5_000, exactly = 2) { stream.subscribe(any()) }
        job.cancel()
    }

    @Test
    fun `an identity change re-dials the stream`() = runBlocking {
        val stream = mockk<NotificationsStream>()
        every { stream.subscribe(any()) } returns openStream()
        val repo = NotificationsRepository(stream, prefs())

        val job = launch(Dispatchers.Default) { repo.subscribeProgressUpdates().collect { } }
        verify(timeout = 5_000, exactly = 1) { stream.subscribe(any()) }
        repo.restartForIdentityChange()
        verify(timeout = 5_000, exactly = 2) { stream.subscribe(any()) }
        job.cancel()
    }

    @Test
    fun `two subscribers share one connection`() = runBlocking {
        val stream = mockk<NotificationsStream>()
        every { stream.subscribe(any()) } returns openStream()
        val repo = NotificationsRepository(stream, prefs())

        val a = launch(Dispatchers.Default) { repo.subscribePlaybackTransfers().collect { } }
        val b = launch(Dispatchers.Default) { repo.subscribeProgressUpdates().collect { } }
        verify(timeout = 5_000, exactly = 1) { stream.subscribe(any()) }
        a.cancel()
        b.cancel()
        verify(exactly = 1) { stream.subscribe(any()) }
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
        advanceTimeBy(60 * 60_000L) // an hour with the server down
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
