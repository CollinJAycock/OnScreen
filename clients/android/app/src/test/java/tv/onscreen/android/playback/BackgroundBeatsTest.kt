package tv.onscreen.android.playback

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.android.data.api.HeartbeatRefusal

/** The background service's heartbeats: never awaited by the beat loop,
 *  bounded, not doubled up, and out of the way of the final report. */
class BackgroundBeatsTest {

    private fun http403() = retrofit2.HttpException(
        retrofit2.Response.error<Unit>(
            403,
            okhttp3.ResponseBody.create(null, """{"error":{"code":"PARENTAL_LIMIT","message":"daily_limit_reached"}}"""),
        ),
    )

    @Test
    fun `a hung beat skips the next tick, then is cut off at the timeout`() = runTest(StandardTestDispatcher()) {
        val beats = BackgroundBeats(ReportLane(this), this, timeoutMs = 15_000, graceMs = 2_000)
        var sent = 0
        var cancelled = false

        assertThat(beats.beat(send = {
            sent++
            try { awaitCancellation() } finally { cancelled = true }
        }, onRefused = {})).isTrue()
        runCurrent()
        assertThat(sent).isEqualTo(1)

        // The next tick, 10 s on: still out, so this one is skipped.
        advanceTimeBy(10_000)
        assertThat(beats.beat(send = { sent++ }, onRefused = {})).isFalse()

        // At 15 s the hung PUT is cancelled, and the next tick goes out.
        advanceTimeBy(5_001)
        assertThat(cancelled).isTrue()
        assertThat(beats.busy).isFalse()
        assertThat(beats.beat(send = { sent++ }, onRefused = {})).isTrue()
        advanceUntilIdle()
        assertThat(sent).isEqualTo(2)
    }

    @Test
    fun `a refused beat calls back, other failures stay quiet`() = runTest(StandardTestDispatcher()) {
        val beats = BackgroundBeats(ReportLane(this), this)
        val refusals = mutableListOf<HeartbeatRefusal>()
        beats.beat(send = { throw http403() }, onRefused = { refusals += it })
        advanceUntilIdle()
        assertThat(refusals).containsExactly(HeartbeatRefusal.WatchLimit("daily_limit_reached"))

        beats.beat(send = { throw java.io.IOException("offline") }, onRefused = { refusals += it })
        advanceUntilIdle()
        assertThat(refusals).hasSize(1)
    }

    @Test
    fun `a final report waits for a beat only for the grace period`() = runTest(StandardTestDispatcher()) {
        val lane = ReportLane(this)
        val beats = BackgroundBeats(lane, this, timeoutMs = 15_000, graceMs = 2_000)
        val events = mutableListOf<String>()

        beats.beat(send = { events += "beat"; awaitCancellation() }, onRefused = {})
        runCurrent()
        beats.beforeTerminal()
        lane.launch { events += "paused" }

        advanceTimeBy(1_999)
        assertThat(events).containsExactly("beat")
        advanceTimeBy(2)
        assertThat(events).containsExactly("beat", "paused").inOrder()
    }

    @Test
    fun `an ordinary beat still lands before the final report`() = runTest(StandardTestDispatcher()) {
        val lane = ReportLane(this)
        val beats = BackgroundBeats(lane, this)
        val events = mutableListOf<String>()
        beats.beat(send = { delay(300); events += "beat" }, onRefused = {})
        beats.beforeTerminal()
        lane.launch { events += "stopped" }
        advanceUntilIdle()
        assertThat(events).containsExactly("beat", "stopped").inOrder()
    }
}
