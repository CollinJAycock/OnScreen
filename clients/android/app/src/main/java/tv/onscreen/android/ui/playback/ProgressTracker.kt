package tv.onscreen.android.ui.playback

import kotlinx.coroutines.*
import retrofit2.HttpException
import tv.onscreen.android.data.api.apiError
import tv.onscreen.android.data.repository.ItemRepository

/**
 * Periodically reports playback progress to the server.
 * Runs every 10 seconds while playing, and fires on pause/stop events.
 */
class ProgressTracker(
    private val scope: CoroutineScope,
    private val itemRepo: ItemRepository,
    // Scope for the terminal pause/stop reports. The injected [scope] is the
    // fragment's viewLifecycleOwner.lifecycleScope, cancelled the instant the
    // view is destroyed — so a `paused`/`stopped` report launched there during
    // teardown (onStop → onDestroyView) could be cancelled before the PUT leaves
    // the device, losing the final position. This default outlives the view so
    // the last position persists (SupervisorJob so one failed report doesn't
    // poison the next). Injectable so tests can drive it with a test dispatcher.
    private val terminalScope: CoroutineScope =
        CoroutineScope(SupervisorJob() + Dispatchers.IO),
) {
    private var job: Job? = null
    private var itemId: String? = null
    private var hlsOffsetMs: Long = 0

    /** Position provider — returns the raw player position in ms. */
    var positionProvider: (() -> Long)? = null

    /** Duration provider — returns the total duration in ms. */
    var durationProvider: (() -> Long)? = null

    /** Fires (on the main thread) when a 'playing' heartbeat is rejected by
     *  the parental watch limit — a daily cap reached or the allowed-hours
     *  window closing mid-session. The tracker stops itself first; the caller
     *  pauses playback and shows the block message. */
    var onBlocked: ((reason: String) -> Unit)? = null

    fun start(itemId: String, hlsOffsetMs: Long = 0) {
        this.itemId = itemId
        this.hlsOffsetMs = hlsOffsetMs
        job?.cancel()
        job = scope.launch {
            while (isActive) {
                delay(10_000)
                // The heartbeat runs on the (main) lifecycle scope, so reading
                // the player via snapshot() here is already on the right thread.
                val snap = snapshot() ?: continue
                report("playing", snap)
            }
        }
    }

    fun onPause() {
        job?.cancel()
        // Snapshot the player position on the CURRENT (main) thread, before
        // launching the report. ExoPlayer must be accessed on its main thread,
        // and on the stop path the fragment releases + nulls the player
        // synchronously right after this call — so the read must happen here,
        // not inside the (background) terminal coroutine.
        val snap = snapshot() ?: return
        // Launch on the survivable scope: onPause often coincides with view
        // teardown, and the final position must persist even though the view
        // scope is being cancelled.
        terminalScope.launch { report("paused", snap) }
    }

    fun onStop() {
        job?.cancel()
        val snap = snapshot() ?: return
        terminalScope.launch { report("stopped", snap) }
    }

    fun stop() {
        job?.cancel()
        job = null
    }

    fun updateOffset(offsetMs: Long) {
        this.hlsOffsetMs = offsetMs
    }

    /** Content position reported by the most recent successful publish.
     *  Used by the cross-device sync subscriber as a self-loop guard —
     *  the same Progress PUT round-trips back as a `progress.updated`
     *  SSE event, and the subscriber ignores echoes that match this
     *  value within a small tolerance. -1 until the first publish. */
    @Volatile
    var lastReportedContentMs: Long = -1L
        private set

    /**
     * Reads the position/duration providers into a snapshot. MUST be called on
     * the main thread — the providers touch the live ExoPlayer, which Media3
     * requires be accessed only on its creation (main) thread. Returns null when
     * a provider is not yet set.
     */
    private fun snapshot(): Pair<Long, Long>? {
        val rawPos = positionProvider?.invoke() ?: return null
        val dur = durationProvider?.invoke() ?: return null
        return rawPos to dur
    }

    private suspend fun report(state: String, snapshot: Pair<Long, Long>) {
        val id = itemId ?: return
        val (rawPos, dur) = snapshot
        if (dur <= 0) return

        val contentPos = rawPos + hlsOffsetMs
        try {
            itemRepo.updateProgress(id, contentPos, dur, state)
            lastReportedContentMs = contentPos
        } catch (e: Exception) {
            // A 'playing' heartbeat rejected with a parental watch-limit 403
            // means the cap was reached (or the allowed-hours window closed)
            // mid-session — stop reporting and surface the block. Any other
            // failure stays best-effort (don't crash playback on a hiccup).
            if (state == "playing" && e is HttpException && e.code() == 403) {
                val err = e.apiError()
                if (err?.code == "PARENTAL_LIMIT") {
                    // Dispatch the callback on terminalScope, NOT from this
                    // coroutine: this code runs inside the heartbeat job that
                    // stop() is about to cancel, and withContext() begins with
                    // ensureActive() — so dispatching from here after stop()
                    // threw CancellationException before the callback ever ran,
                    // and the block screen never appeared. terminalScope
                    // outlives the job by design.
                    val cb = onBlocked
                    if (cb != null) {
                        terminalScope.launch(Dispatchers.Main) { cb(err.message ?: "") }
                    }
                    stop()
                }
            }
        }
    }
}
