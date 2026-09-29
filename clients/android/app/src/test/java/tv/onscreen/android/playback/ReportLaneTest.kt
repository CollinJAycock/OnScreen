package tv.onscreen.android.playback

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.CoroutineExceptionHandler
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.StandardTestDispatcher
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
}
