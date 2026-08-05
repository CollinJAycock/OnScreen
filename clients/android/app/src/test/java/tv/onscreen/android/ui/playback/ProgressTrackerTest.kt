package tv.onscreen.android.ui.playback

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.device.ClientName
import tv.onscreen.android.data.repository.ItemRepository
import java.lang.reflect.Proxy
import java.util.concurrent.Executors

@OptIn(ExperimentalCoroutinesApi::class)
class ProgressTrackerTest {

    companion object {
        /** Dynamic proxy satisfying the API interface — never invoked by these tests. */
        private val FakeApi: OnScreenApi = Proxy.newProxyInstance(
            OnScreenApi::class.java.classLoader,
            arrayOf(OnScreenApi::class.java),
        ) { _, method, _ -> error("unexpected API call: ${method.name}") } as OnScreenApi
    }

    /**
     * Minimal fake [ItemRepository]: records every `updateProgress` invocation
     * and can be configured to throw on the next call.
     */
    private class FakeRepo : ItemRepository(FakeApi, ClientName(null)) {
        val calls = mutableListOf<Call>()
        var throwNext: Throwable? = null

        data class Call(val itemId: String, val offsetMs: Long, val durationMs: Long, val state: String)

        override suspend fun updateProgress(
            itemId: String,
            offsetMs: Long,
            durationMs: Long,
            state: String,
        ) {
            throwNext?.let { throw it }
            calls += Call(itemId, offsetMs, durationMs, state)
        }
    }

    private fun newTracker(
        repo: ItemRepository,
        scope: CoroutineScope,
    ): ProgressTracker = ProgressTracker(scope, repo, scope).apply {
        positionProvider = { 5_000L }
        durationProvider = { 60_000L }
    }

    @Test
    fun `start fires periodic playing reports every 10 seconds`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this)

        tracker.start("item-1")

        advanceTimeBy(10_001)
        runCurrent()
        assertThat(repo.calls.filter { it.state == "playing" }).hasSize(1)

        advanceTimeBy(10_000)
        runCurrent()
        assertThat(repo.calls.filter { it.state == "playing" }).hasSize(2)

        repo.calls.filter { it.state == "playing" }.forEach {
            assertThat(it.itemId).isEqualTo("item-1")
            assertThat(it.offsetMs).isEqualTo(5_000L)
            assertThat(it.durationMs).isEqualTo(60_000L)
        }

        tracker.stop()
    }

    @Test
    fun `onPause fires single paused report and cancels periodic job`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this)

        tracker.start("item-1")
        advanceTimeBy(11_000)
        runCurrent()
        assertThat(repo.calls.count { it.state == "playing" }).isEqualTo(1)

        tracker.onPause()
        runCurrent()
        assertThat(repo.calls.count { it.state == "paused" }).isEqualTo(1)

        // After pause, no more periodic playing reports should fire.
        advanceTimeBy(30_000)
        runCurrent()
        assertThat(repo.calls.count { it.state == "playing" }).isEqualTo(1)
    }

    @Test
    fun `onStop fires stopped report`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this)

        tracker.start("item-7")
        runCurrent()
        tracker.onStop()
        runCurrent()

        assertThat(repo.calls.count { it.itemId == "item-7" && it.state == "stopped" }).isEqualTo(1)
    }

    @Test
    fun `hlsOffsetMs is added to player position before reporting`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this)

        tracker.start("item-1", hlsOffsetMs = 30_000L)
        advanceTimeBy(11_000)
        runCurrent()

        val call = repo.calls.first { it.state == "playing" }
        // 5_000 player pos + 30_000 hls offset = 35_000 content pos.
        assertThat(call.offsetMs).isEqualTo(35_000L)
        assertThat(call.durationMs).isEqualTo(60_000L)

        tracker.stop()
    }

    @Test
    fun `updateOffset changes the offset for subsequent reports`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this)

        tracker.start("item-1", hlsOffsetMs = 0L)
        tracker.updateOffset(45_000L)

        tracker.onPause()
        runCurrent()

        val call = repo.calls.first { it.state == "paused" }
        assertThat(call.offsetMs).isEqualTo(50_000L)
    }

    @Test
    fun `report is skipped when duration is zero`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = ProgressTracker(this, repo, this).apply {
            positionProvider = { 5_000L }
            durationProvider = { 0L }
        }

        tracker.start("item-1")
        advanceTimeBy(11_000)
        runCurrent()

        assertThat(repo.calls).isEmpty()
        tracker.stop()
    }

    @Test
    fun `report is skipped when providers are not set`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = ProgressTracker(this, repo, this)

        tracker.start("item-1")
        tracker.onPause()
        runCurrent()

        assertThat(repo.calls).isEmpty()
    }

    @Test
    fun `repository exceptions are swallowed so playback is not affected`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo().apply { throwNext = RuntimeException("network down") }
        val tracker = newTracker(repo, this)

        tracker.start("item-1")
        // Should not throw.
        advanceTimeBy(11_000)
        runCurrent()
        tracker.onPause()
        runCurrent()
        // No crashes, no assertions on calls list since all throw.
    }

    @Test
    fun `restarting cancels the previous periodic job`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this)

        tracker.start("item-1")
        advanceTimeBy(11_000)
        runCurrent()

        tracker.start("item-2")
        advanceTimeBy(11_000)
        runCurrent()

        assertThat(repo.calls.count { it.itemId == "item-1" && it.state == "playing" }).isEqualTo(1)
        assertThat(repo.calls.count { it.itemId == "item-2" && it.state == "playing" }).isEqualTo(1)

        tracker.stop()
    }

    @Test
    fun `stop cancels the periodic job without firing a report`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this)

        tracker.start("item-1")
        runCurrent()
        tracker.stop()
        advanceTimeBy(30_000)
        runCurrent()

        assertThat(repo.calls).isEmpty()
    }

    /**
     * Regression guard for the on-device crash
     * `IllegalStateException: Player is accessed on the wrong thread`.
     * The position/duration providers touch the live ExoPlayer, which Media3
     * permits only on its (main) creation thread. onPause()/onStop() must read
     * them via a snapshot on the CALLING thread, BEFORE launching the report on
     * the background terminalScope — never invoke the providers from the
     * terminal IO dispatcher. (Capturing before the launch also preserves the
     * real final position, since the fragment releases the player synchronously
     * right after onStop().)
     */
    @Test
    fun `player position is read on the caller thread, not the terminal IO thread`() {
        val callerThread = Thread.currentThread()
        var providerThread: Thread? = null

        val executor = Executors.newSingleThreadExecutor { r -> Thread(r, "terminal-io") }
        try {
            val terminal = CoroutineScope(SupervisorJob() + executor.asCoroutineDispatcher())
            val tracker = ProgressTracker(terminal, FakeRepo(), terminal).apply {
                positionProvider = {
                    providerThread = Thread.currentThread()
                    5_000L
                }
                durationProvider = { 60_000L }
            }

            // onPause() must snapshot the providers synchronously on this thread.
            tracker.onPause()

            assertThat(providerThread).isEqualTo(callerThread)
            assertThat(providerThread?.name).isNotEqualTo("terminal-io")
        } finally {
            executor.shutdownNow()
        }
    }

    @Test
    fun `PARENTAL_LIMIT 403 on a heartbeat fires onBlocked and stops the tracker`() =
        runTest(StandardTestDispatcher()) {
            // The callback dispatches on terminalScope + Dispatchers.Main —
            // point Main at the test scheduler so the launch is observable.
            Dispatchers.setMain(StandardTestDispatcher(testScheduler))
            try {
                val repo = FakeRepo()
                val tracker = newTracker(repo, this)
                var blockedReason: String? = null
                tracker.onBlocked = { blockedReason = it }

                tracker.start("item-1")
                advanceTimeBy(10_001)
                runCurrent()
                assertThat(repo.calls.filter { it.state == "playing" }).hasSize(1)

                // Server rejects the next heartbeat: daily cap reached.
                repo.throwNext = retrofit2.HttpException(
                    retrofit2.Response.error<Unit>(
                        403,
                        okhttp3.ResponseBody.create(
                            null,
                            """{"error":{"code":"PARENTAL_LIMIT","message":"daily_limit_reached"}}""",
                        ),
                    ),
                )
                advanceTimeBy(10_000)
                runCurrent()

                // The regression this pins: stop() cancels the heartbeat
                // coroutine the handler is RUNNING IN, and the old code then
                // dispatched the callback via withContext from that same
                // coroutine — ensureActive() threw and onBlocked never fired,
                // so a restricted profile crossing its cap mid-episode saw
                // nothing at all.
                assertThat(blockedReason).isEqualTo("daily_limit_reached")

                // Tracker stopped itself: no further heartbeats.
                repo.throwNext = null
                val before = repo.calls.size
                advanceTimeBy(30_000)
                runCurrent()
                assertThat(repo.calls.size).isEqualTo(before)
            } finally {
                Dispatchers.resetMain()
            }
        }
}
