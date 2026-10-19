package tv.onscreen.mobile.playback

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.mobile.data.api.HeartbeatRefusal

/** The background service's heartbeats: never awaited, bounded, not doubled up. */
class BoundedBeatTest {

    @Test
    fun `a hung beat skips the next tick, then is cut off at the timeout`() = runTest(StandardTestDispatcher()) {
        val beats = BoundedBeat(this, timeoutMs = 15_000)
        var sent = 0
        var cancelled = false

        assertThat(beats.beat(send = {
            sent++
            try { awaitCancellation() } finally { cancelled = true }
        }, onRefused = {})).isTrue()
        runCurrent()
        advanceTimeBy(10_000)
        assertThat(beats.beat(send = { sent++ }, onRefused = {})).isFalse()

        advanceTimeBy(5_001)
        assertThat(cancelled).isTrue()
        assertThat(beats.busy).isFalse()
        assertThat(beats.beat(send = { sent++ }, onRefused = {})).isTrue()
        advanceUntilIdle()
        assertThat(sent).isEqualTo(2)
    }

    @Test
    fun `a refused beat calls back, other failures stay quiet`() = runTest(StandardTestDispatcher()) {
        val beats = BoundedBeat(this)
        val refusals = mutableListOf<HeartbeatRefusal>()
        val forbidden = retrofit2.HttpException(
            retrofit2.Response.error<Unit>(403, okhttp3.ResponseBody.create(null, "")),
        )
        beats.beat(send = { throw forbidden }, onRefused = { refusals += it })
        advanceUntilIdle()
        assertThat(refusals).hasSize(1)

        beats.beat(send = { throw java.io.IOException("offline") }, onRefused = { refusals += it })
        advanceUntilIdle()
        assertThat(refusals).hasSize(1)
    }

    @Test
    fun `cancel drops a beat still out`() = runTest(StandardTestDispatcher()) {
        val beats = BoundedBeat(this)
        beats.beat(send = { awaitCancellation() }, onRefused = {})
        runCurrent()
        assertThat(beats.busy).isTrue()
        beats.cancel()
        assertThat(beats.busy).isFalse()
    }
}
