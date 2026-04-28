package tv.onscreen.android.ui.playback

import kotlinx.coroutines.*
import tv.onscreen.android.data.repository.ItemRepository

/**
 * Periodically reports playback progress to the server.
 * Runs every 10 seconds while playing, and fires on pause/stop events.
 */
class ProgressTracker(
    private val scope: CoroutineScope,
    private val itemRepo: ItemRepository,
) {
    private var job: Job? = null
    private var itemId: String? = null
    private var hlsOffsetMs: Long = 0

    /** Position provider — returns the raw player position in ms. */
    var positionProvider: (() -> Long)? = null

    /** Duration provider — returns the total duration in ms. */
    var durationProvider: (() -> Long)? = null

    fun start(itemId: String, hlsOffsetMs: Long = 0) {
        this.itemId = itemId
        this.hlsOffsetMs = hlsOffsetMs
        job?.cancel()
        job = scope.launch {
            while (isActive) {
                delay(10_000)
                report("playing")
            }
        }
    }

    fun onPause() {
        job?.cancel()
        scope.launch { report("paused") }
    }

    fun onStop() {
        job?.cancel()
        scope.launch { report("stopped") }
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

    private suspend fun report(state: String) {
        val id = itemId ?: return
        val rawPos = positionProvider?.invoke() ?: return
        val dur = durationProvider?.invoke() ?: return
        if (dur <= 0) return

        val contentPos = rawPos + hlsOffsetMs
        try {
            itemRepo.updateProgress(id, contentPos, dur, state)
            lastReportedContentMs = contentPos
        } catch (_: Exception) {
            // Best-effort — don't crash playback if server is unreachable.
        }
    }
}
