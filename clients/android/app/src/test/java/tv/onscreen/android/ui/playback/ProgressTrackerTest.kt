package tv.onscreen.android.ui.playback

import androidx.media3.common.Player
import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
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
        /** How long the server takes to answer each state, in ms. */
        var slow: Map<String, Long> = emptyMap()
        /** How long it takes to answer a report of a content position, in
         *  ms, whatever its state. */
        var slowAt: Map<Long, Long> = emptyMap()

        data class Call(val itemId: String, val offsetMs: Long, val durationMs: Long, val state: String)

        override suspend fun updateProgress(
            itemId: String,
            offsetMs: Long,
            durationMs: Long,
            state: String,
        ) {
            throwNext?.let { throw it }
            slow[state]?.let { delay(it) }
            slowAt[offsetMs]?.let { delay(it) }
            calls += Call(itemId, offsetMs, durationMs, state)
        }
    }

    /** A tracker on [scope]'s virtual time, sending on [reports]. */
    private fun newTracker(
        repo: ItemRepository,
        scope: TestScope,
        reports: ProgressTracker.Reports = ProgressTracker.Reports(scope),
    ): ProgressTracker = ProgressTracker(
        scope,
        repo,
        scope,
        nowMs = { scope.testScheduler.currentTime },
        reports = reports,
    ).apply {
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
                assertThat(blockedReason).isEqualTo("watch_limit:daily_limit_reached")

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

    private fun http403(body: String?) = retrofit2.HttpException(
        retrofit2.Response.error<Unit>(
            403,
            okhttp3.ResponseBody.create(null, body ?: ""),
        ),
    )

    private fun http(code: Int) = retrofit2.HttpException(
        retrofit2.Response.error<Unit>(code, okhttp3.ResponseBody.create(null, "")),
    )

    /** Drives one successful heartbeat, then fails the next with [failure]
     *  and returns what onBlocked received (null = never fired) plus whether
     *  heartbeats continued afterwards. */
    private fun kotlinx.coroutines.test.TestScope.heartbeatRejectedWith(
        failure: Throwable,
    ): Pair<String?, Boolean> {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this)
        var blocked: String? = null
        tracker.onBlocked = { blocked = it }

        tracker.start("item-1")
        advanceTimeBy(10_001)
        runCurrent()

        repo.throwNext = failure
        advanceTimeBy(10_000)
        runCurrent()

        repo.throwNext = null
        val before = repo.calls.size
        advanceTimeBy(30_000)
        runCurrent()
        val keptReporting = repo.calls.size > before
        tracker.stop()
        return blocked to keptReporting
    }

    @Test
    fun `non-parental 403 on a heartbeat also tears playback down`() =
        runTest(StandardTestDispatcher()) {
            Dispatchers.setMain(StandardTestDispatcher(testScheduler))
            try {
                // checkLibraryAccess: library grant revoked or rating ceiling
                // lowered while this was playing. Used to be swallowed as a
                // best-effort hiccup, so the stream kept going on its token.
                val (blocked, keptReporting) = heartbeatRejectedWith(
                    http403("""{"error":{"code":"FORBIDDEN","message":"access denied"}}"""),
                )
                assertThat(blocked).isEqualTo(ProgressTracker.CONTENT_REVOKED)
                assertThat(keptReporting).isFalse()
            } finally {
                Dispatchers.resetMain()
            }
        }

    @Test
    fun `PLAYBACK_STOPPED 403 on a heartbeat fires the admin-stop sentinel with the server sentence`() =
        runTest(StandardTestDispatcher()) {
            Dispatchers.setMain(StandardTestDispatcher(testScheduler))
            try {
                // Admin stop from Now Playing: the server refuses the stopped
                // stream's 'playing' beacon for the stop window.
                val (blocked, keptReporting) = heartbeatRejectedWith(
                    http403(
                        """{"error":{"code":"PLAYBACK_STOPPED",""" +
                            """"message":"Playback was stopped by the server admin: bedtime"}}""",
                    ),
                )
                assertThat(blocked)
                    .isEqualTo("playback_stopped:Playback was stopped by the server admin: bedtime")
                assertThat(tv.onscreen.android.data.api.PlaybackStop.isSentinel(blocked)).isTrue()
                assertThat(keptReporting).isFalse()
            } finally {
                Dispatchers.resetMain()
            }
        }

    @Test
    fun `403 without an error envelope still tears playback down`() =
        runTest(StandardTestDispatcher()) {
            Dispatchers.setMain(StandardTestDispatcher(testScheduler))
            try {
                val (blocked, keptReporting) = heartbeatRejectedWith(http403(null))
                assertThat(blocked).isEqualTo(ProgressTracker.CONTENT_REVOKED)
                assertThat(keptReporting).isFalse()
            } finally {
                Dispatchers.resetMain()
            }
        }

    @Test
    fun `non-403 heartbeat failures stay best-effort`() =
        runTest(StandardTestDispatcher()) {
            Dispatchers.setMain(StandardTestDispatcher(testScheduler))
            try {
                for (failure in listOf(http(500), http(404), RuntimeException("network down"))) {
                    val (blocked, keptReporting) = heartbeatRejectedWith(failure)
                    assertThat(blocked).isNull()
                    assertThat(keptReporting).isTrue()
                }
            } finally {
                Dispatchers.resetMain()
            }
        }

    // ── probeRefusal (stream died under the player) ─────────────────────────

    @Test
    fun `probe after the stream died reports the admin stop even though the tracker is paused`() =
        runTest(StandardTestDispatcher()) {
            val repo = FakeRepo()
            val tracker = newTracker(repo, this)
            var fired: String? = null
            tracker.onBlocked = { fired = it }
            tracker.start("item-1", hlsOffsetMs = 1_000L)
            // The stopped stream's 404 paused the player → the heartbeat paused.
            tracker.onPause()
            runCurrent()

            repo.throwNext = http403(
                """{"error":{"code":"PLAYBACK_STOPPED",""" +
                    """"message":"Playback was stopped by the server admin: bedtime"}}""",
            )
            val sentinel = tracker.probeRefusal()

            assertThat(sentinel)
                .isEqualTo("playback_stopped:Playback was stopped by the server admin: bedtime")
            // The caller acts on the return value; the callback stays quiet.
            assertThat(fired).isNull()
            // No further heartbeats after a refusal.
            repo.throwNext = null
            val before = repo.calls.size
            advanceTimeBy(30_000)
            runCurrent()
            assertThat(repo.calls.size).isEqualTo(before)
        }

    @Test
    fun `probe sends one playing beat at the content position and returns null when accepted`() =
        runTest(StandardTestDispatcher()) {
            val repo = FakeRepo()
            val tracker = newTracker(repo, this)
            tracker.start("item-1", hlsOffsetMs = 1_000L)
            tracker.stop()

            assertThat(tracker.probeRefusal()).isNull()
            assertThat(repo.calls).containsExactly(FakeRepo.Call("item-1", 6_000L, 60_000L, "playing"))
        }

    @Test
    fun `probe failures other than a 403 are not refusals`() =
        runTest(StandardTestDispatcher()) {
            for (failure in listOf(http(404), http(500), RuntimeException("network down"))) {
                val repo = FakeRepo().apply { throwNext = failure }
                val tracker = newTracker(repo, this)
                tracker.start("item-1")
                assertThat(tracker.probeRefusal()).isNull()
                tracker.stop()
            }
        }

    @Test
    fun `probe before the tracker ever started sends nothing`() =
        runTest(StandardTestDispatcher()) {
            val repo = FakeRepo()
            val tracker = newTracker(repo, this)
            assertThat(tracker.probeRefusal()).isNull()
            assertThat(repo.calls).isEmpty()
        }

    @Test
    fun `a 403 on a paused report does not fire onBlocked`() =
        runTest(StandardTestDispatcher()) {
            Dispatchers.setMain(StandardTestDispatcher(testScheduler))
            try {
                val repo = FakeRepo().apply { throwNext = http403(null) }
                val tracker = newTracker(repo, this)
                var blocked: String? = null
                tracker.onBlocked = { blocked = it }

                // Only 'playing' heartbeats are gated; a terminal pause/stop
                // report being refused is not a reason to show a block dialog.
                tracker.onPause()
                runCurrent()

                assertThat(blocked).isNull()
            } finally {
                Dispatchers.resetMain()
            }
        }

    @Test
    fun `a bound tracker sends nothing until playback starts, but still reports a stop`() =
        runTest(StandardTestDispatcher()) {
            val repo = FakeRepo()
            val tracker = newTracker(repo, this)

            tracker.bind("item-1")
            advanceTimeBy(30_001)
            runCurrent()
            assertThat(repo.calls).isEmpty()

            tracker.onStop()
            runCurrent()
            assertThat(repo.calls.map { it.state to it.itemId }).containsExactly("stopped" to "item-1")
        }

    @Test
    fun `a teardown's repeated reports go out once each`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this)
        tracker.start("item-1")

        // The player's own pause callback, the fragment's onPause, then onStop.
        tracker.onPause()
        tracker.onPause()
        tracker.onStop()
        tracker.onStop()
        runCurrent()

        assertThat(repo.calls.map { it.state }).containsExactly("paused", "stopped").inOrder()
    }

    @Test
    fun `a slow pause report still lands before the stop after it`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo().apply { slow = mapOf("paused" to 500L) }
        val tracker = newTracker(repo, this)
        tracker.start("item-1")

        // BACK out of a video: onPause, then onStop a few ms later.
        tracker.onPause()
        tracker.onStop()
        advanceUntilIdle()

        assertThat(repo.calls.map { it.state }).containsExactly("paused", "stopped").inOrder()
    }

    @Test
    fun `a pause after playing again is reported again`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this)
        tracker.start("item-1")
        tracker.onPause()
        tracker.start("item-1")
        tracker.onPause()
        runCurrent()

        assertThat(repo.calls.filter { it.state == "paused" }).hasSize(2)
    }

    @Test
    fun `a position past the listed length is reported at the length`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this).apply { positionProvider = { 61_500L } }
        tracker.start("item-1")
        tracker.onStop()
        runCurrent()

        assertThat(repo.calls.single().offsetMs).isEqualTo(60_000L)
    }

    // ── heartbeatFor: what a player change does to the heartbeat ────────────

    private fun heartbeat(
        isPlaying: Boolean = false,
        playWhenReady: Boolean = true,
        state: Int = Player.STATE_READY,
        heldForFrameRate: Boolean = false,
    ) = ProgressTracker.heartbeatFor(isPlaying, playWhenReady, state, heldForFrameRate)

    @Test
    fun `playing runs the heartbeat`() {
        assertThat(heartbeat(isPlaying = true)).isEqualTo(ProgressTracker.Heartbeat.START)
    }

    @Test
    fun `a rebuffer holds it without a pause`() {
        assertThat(heartbeat(state = Player.STATE_BUFFERING)).isEqualTo(ProgressTracker.Heartbeat.HOLD)
        // Nor is the display-switch hold a pause of the user's.
        assertThat(heartbeat(playWhenReady = false, heldForFrameRate = true))
            .isEqualTo(ProgressTracker.Heartbeat.HOLD)
    }

    @Test
    fun `a pause is a pause, while buffering too`() {
        assertThat(heartbeat(playWhenReady = false)).isEqualTo(ProgressTracker.Heartbeat.PAUSE)
        assertThat(heartbeat(playWhenReady = false, state = Player.STATE_BUFFERING))
            .isEqualTo(ProgressTracker.Heartbeat.PAUSE)
        // Stopped playing while ready (suppressed: another app took the
        // audio), or failed: not waiting for data.
        assertThat(heartbeat(state = Player.STATE_READY)).isEqualTo(ProgressTracker.Heartbeat.PAUSE)
        assertThat(heartbeat(state = Player.STATE_IDLE)).isEqualTo(ProgressTracker.Heartbeat.PAUSE)
    }

    @Test
    fun `the end reports itself`() {
        assertThat(heartbeat(state = Player.STATE_ENDED)).isEqualTo(ProgressTracker.Heartbeat.NONE)
        assertThat(heartbeat(playWhenReady = false, state = Player.STATE_ENDED))
            .isEqualTo(ProgressTracker.Heartbeat.NONE)
    }

    /** One BUFFERING / READY flap as the fragment follows it. */
    private fun ProgressTracker.flap(stallMs: Long, playMs: Long, scope: TestScope) {
        follow(heartbeat(state = Player.STATE_BUFFERING), "item-1", 0L)
        scope.advanceTimeBy(stallMs)
        follow(heartbeat(isPlaying = true), "item-1", 0L)
        scope.advanceTimeBy(playMs)
    }

    @Test
    fun `a stall flapping between buffering and ready sends no pause, and the heartbeat keeps its beat`() =
        runTest(StandardTestDispatcher()) {
            // The Fire TV remux stall: BUFFERING / READY every ~0.7 s. A
            // 'paused' per flap sent five PUTs in 4 s; restarting the 10 s
            // count on every flap sent no heartbeat at all while it lasted.
            val repo = FakeRepo()
            val tracker = newTracker(repo, this)
            tracker.follow(heartbeat(isPlaying = true), "item-1", 0L)
            repeat(18) { tracker.flap(stallMs = 700, playMs = 700, scope = this) } // 25.2 s
            runCurrent()

            assertThat(repo.calls.map { it.state }).containsExactly("playing", "playing")

            // A real pause afterwards is still reported, once.
            tracker.follow(heartbeat(playWhenReady = false), "item-1", 0L)
            runCurrent()
            assertThat(repo.calls.map { it.state }).containsExactly("playing", "playing", "paused").inOrder()
        }

    @Test
    fun `a stall whose ready spells are shorter than the round trip still lands its beats`() =
        runTest(StandardTestDispatcher()) {
            // Each READY spell (700 ms) is shorter than the server takes to
            // answer a 'playing' PUT (1 s). With the PUT inside the heartbeat
            // job, the BUFFERING hold after it cancelled every beat in
            // flight, and the due time had already moved on: none landed.
            val repo = FakeRepo().apply { slow = mapOf("playing" to 1_000L) }
            val tracker = newTracker(repo, this)
            tracker.follow(heartbeat(isPlaying = true), "item-1", 0L)
            repeat(18) { tracker.flap(stallMs = 700, playMs = 700, scope = this) } // 25.2 s
            advanceTimeBy(1_000)
            runCurrent()

            assertThat(repo.calls.map { it.state }).containsExactly("playing", "playing")
            tracker.stop()
        }

    @Test
    fun `a beat due while the last one is still in flight is skipped`() = runTest(StandardTestDispatcher()) {
        // A server taking 15 s to answer: the 20 s beat finds the 10 s one
        // still out and doesn't queue behind it; the 30 s one goes.
        val repo = FakeRepo().apply { slow = mapOf("playing" to 15_000L) }
        val tracker = newTracker(repo, this)
        tracker.start("item-1")
        advanceTimeBy(40_001)
        runCurrent()
        assertThat(repo.calls).hasSize(1) // the 10 s beat, answered at 25 s

        advanceTimeBy(5_000)
        runCurrent()
        assertThat(repo.calls).hasSize(2) // the 30 s beat, answered at 45 s
        tracker.stop()
    }

    @Test
    fun `a pause after a slow beat lands after it`() = runTest(StandardTestDispatcher()) {
        // The pause cancels the heartbeat, not the beat already out; the
        // 'paused' waits for it on the lane. Landing after the 'paused', the
        // 'playing' would put the item back in Now Playing as playing.
        val repo = FakeRepo().apply { slow = mapOf("playing" to 1_000L) }
        val tracker = newTracker(repo, this)
        tracker.start("item-1")
        advanceTimeBy(10_500) // the beat is out
        tracker.onPause()
        advanceUntilIdle()

        assertThat(repo.calls.map { it.state }).containsExactly("playing", "paused").inOrder()
    }

    @Test
    fun `a teardown waits only so long for a hung beat`() = runTest(StandardTestDispatcher()) {
        // A server that never answers the beat: the teardown's reports went
        // out only when its call timed out (30 s). They get the beat's
        // ordinary round trip now, then it is cancelled and they go.
        val repo = FakeRepo().apply { slow = mapOf("playing" to 30_000L) }
        val tracker = newTracker(repo, this)
        tracker.start("item-1")
        advanceTimeBy(10_500) // the beat is out
        tracker.onPause()
        tracker.onStop()
        advanceTimeBy(ProgressTracker.BEAT_GRACE_MS - 1)
        runCurrent()
        assertThat(repo.calls).isEmpty()

        advanceTimeBy(1)
        runCurrent()
        assertThat(repo.calls.map { it.state }).containsExactly("paused", "stopped").inOrder()

        // And the beat never lands after them.
        advanceUntilIdle()
        assertThat(repo.calls.map { it.state }).containsExactly("paused", "stopped").inOrder()
    }

    @Test
    fun `a beat whose grace runs out while it waits behind a slow pause keeps its place`() =
        runTest(StandardTestDispatcher()) {
            // A pause at 5 s that the server takes 15 s to answer; played on,
            // the next beat queues behind it. BACK then queues a pause and a
            // stop behind the beat, whose grace runs out while it still
            // waits: it goes unsent, but cancelled out of its wait it let the
            // two behind it out ahead of the first pause, which landed last
            // and left the item paused at 5 s.
            val repo = FakeRepo().apply { slowAt = mapOf(5_000L to 15_000L) }
            val tracker = newTracker(repo, this)
            var position = 5_000L
            tracker.positionProvider = { position }
            tracker.start("item-1")
            tracker.onPause() // out until 15 s
            position = 6_000L
            tracker.start("item-1")
            advanceTimeBy(10_001) // the beat, queued behind the pause
            position = 7_000L
            tracker.onPause()
            tracker.onStop()
            advanceUntilIdle()

            assertThat(repo.calls.map { it.state to it.offsetMs })
                .containsExactly("paused" to 5_000L, "paused" to 7_000L, "stopped" to 7_000L)
                .inOrder()
        }

    @Test
    fun `a replaced tracker's slow beat lands before the new tracker's stop`() = runTest(StandardTestDispatcher()) {
        // A session re-issue installs a new tracker while the old one's beat
        // is out. With a lane each, the new tracker's 'stopped' went straight
        // out and the old 'playing' landed after it, putting the item back
        // in Now Playing.
        val repo = FakeRepo().apply { slow = mapOf("playing" to 1_000L) }
        val screen = ProgressTracker.Reports(this)
        val old = newTracker(repo, this, screen)
        old.start("item-1")
        advanceTimeBy(10_500) // the beat is out
        old.stop() // retired (PlaybackFragment.retireProgressTracker)
        val replacement = newTracker(repo, this, screen)
        replacement.bind("item-1")
        replacement.onStop()
        advanceUntilIdle()

        assertThat(repo.calls.map { it.state }).containsExactly("playing", "stopped").inOrder()
    }

    @Test
    fun `a new tracker's stop waits only so long for a replaced tracker's hung beat`() =
        runTest(StandardTestDispatcher()) {
            val repo = FakeRepo().apply { slow = mapOf("playing" to 30_000L) }
            val screen = ProgressTracker.Reports(this)
            val old = newTracker(repo, this, screen)
            old.start("item-1")
            advanceTimeBy(10_500) // the beat is out
            old.stop()
            val replacement = newTracker(repo, this, screen)
            replacement.bind("item-1")
            replacement.onStop()
            advanceTimeBy(ProgressTracker.BEAT_GRACE_MS - 1)
            runCurrent()
            assertThat(repo.calls).isEmpty()

            advanceTimeBy(1)
            runCurrent()
            assertThat(repo.calls.map { it.state }).containsExactly("stopped")

            // And the old beat never lands after it.
            advanceUntilIdle()
            assertThat(repo.calls.map { it.state }).containsExactly("stopped")
        }

    @Test
    fun `a hold leaves a slow beat to land`() = runTest(StandardTestDispatcher()) {
        // A rebuffer is no teardown: the beat out when it starts still lands,
        // however long the server takes.
        val repo = FakeRepo().apply { slow = mapOf("playing" to 15_000L) }
        val tracker = newTracker(repo, this)
        tracker.start("item-1")
        advanceTimeBy(10_500) // the beat is out
        tracker.follow(ProgressTracker.Heartbeat.HOLD, "item-1", 0L)
        advanceTimeBy(15_000)
        runCurrent()

        assertThat(repo.calls.map { it.state }).containsExactly("playing")
    }

    @Test
    fun `a beat due during a long stall goes at the first ready`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this)
        tracker.start("item-1")
        advanceTimeBy(5_000)
        tracker.stop() // rebuffer at 5 s
        advanceTimeBy(12_000)
        runCurrent()
        assertThat(repo.calls).isEmpty()

        tracker.start("item-1") // ready at 17 s: the 10 s beat is overdue
        runCurrent()
        assertThat(repo.calls.map { it.state }).containsExactly("playing")

        // Then every 10 s from there.
        advanceTimeBy(9_999)
        runCurrent()
        assertThat(repo.calls).hasSize(1)
        advanceTimeBy(1)
        runCurrent()
        assertThat(repo.calls).hasSize(2)
        tracker.stop()
    }

    @Test
    fun `starting again while running keeps the beat`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this)
        tracker.start("item-1")
        advanceTimeBy(6_000)
        tracker.start("item-1")
        advanceTimeBy(4_001)
        runCurrent()
        assertThat(repo.calls).hasSize(1)
        tracker.stop()
    }

    @Test
    fun `a pause starts the count afresh`() = runTest(StandardTestDispatcher()) {
        val repo = FakeRepo()
        val tracker = newTracker(repo, this)
        tracker.start("item-1")
        advanceTimeBy(9_000)
        tracker.onPause()
        tracker.start("item-1")
        advanceTimeBy(9_999)
        runCurrent()
        assertThat(repo.calls.map { it.state }).containsExactly("paused")
        advanceTimeBy(1)
        runCurrent()
        assertThat(repo.calls.map { it.state }).containsExactly("paused", "playing").inOrder()
        tracker.stop()
    }
}
