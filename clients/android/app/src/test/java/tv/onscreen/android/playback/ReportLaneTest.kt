package tv.onscreen.android.playback

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.CoroutineExceptionHandler
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Test

class ReportLaneTest {

    @Test
    fun `a report waits for the one before it`() = runTest(StandardTestDispatcher()) {
        val lane = ReportLane(this)
        val events = mutableListOf<String>()
        lane.launch {
            events += "paused sent"
            delay(500) // a slow server
            events += "paused done"
        }
        lane.launch {
            events += "stopped sent"
            events += "stopped done"
        }
        advanceUntilIdle()
        assertThat(events).containsExactly("paused sent", "paused done", "stopped sent", "stopped done").inOrder()
    }

    @Test
    fun `a failed report doesn't hold up the next`() = runTest(StandardTestDispatcher()) {
        // The scope a lane runs on outlives its reports and swallows their
        // failures, as the app's do.
        val quiet = CoroutineExceptionHandler { _, _ -> }
        val lane = ReportLane(CoroutineScope(SupervisorJob() + StandardTestDispatcher(testScheduler) + quiet))
        val sent = mutableListOf<String>()
        lane.launch { error("server down") }
        lane.launch { sent += "stopped" }
        advanceUntilIdle()
        assertThat(sent).containsExactly("stopped")
    }

    @Test
    fun `a report cancelled while it waits its turn keeps its place`() = runTest(StandardTestDispatcher()) {
        // A slow 'paused', a beat queued behind it, and a 'stopped' behind
        // the beat. Cancelled in its wait, the beat released the 'stopped',
        // which went out ahead of the 'paused'.
        val lane = ReportLane(this)
        val sent = mutableListOf<String>()
        lane.launch {
            delay(5_000)
            sent += "paused"
        }
        val beat = lane.launch { sent += "playing" }
        lane.launch { sent += "stopped" }
        advanceTimeBy(2_000)
        beat.cancel()
        advanceUntilIdle()
        assertThat(sent).containsExactly("paused", "stopped").inOrder()
    }

    @Test
    fun `a report cancelled before it ever ran keeps its place too`() = runTest(StandardTestDispatcher()) {
        val lane = ReportLane(this)
        val sent = mutableListOf<String>()
        lane.launch {
            delay(5_000)
            sent += "paused"
        }
        lane.launch { sent += "playing" }.cancel()
        lane.launch { sent += "stopped" }
        advanceUntilIdle()
        assertThat(sent).containsExactly("paused", "stopped").inOrder()
    }
}
