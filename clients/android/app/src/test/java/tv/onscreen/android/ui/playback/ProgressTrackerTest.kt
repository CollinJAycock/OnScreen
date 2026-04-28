package tv.onscreen.android.ui.playback

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.repository.ItemRepository
import java.lang.reflect.Proxy

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
    private class FakeRepo : ItemRepository(FakeApi) {
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
    ): ProgressTracker = ProgressTracker(scope, repo).apply {
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
        val tracker = ProgressTracker(this, repo).apply {
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
        val tracker = ProgressTracker(this, repo)

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
}
