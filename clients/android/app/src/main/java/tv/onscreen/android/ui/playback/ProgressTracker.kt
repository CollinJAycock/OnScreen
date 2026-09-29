package tv.onscreen.android.ui.playback

import kotlinx.coroutines.*
import tv.onscreen.android.data.api.HeartbeatRefusal
import tv.onscreen.android.data.api.PlaybackStop
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.playback.ReportLane

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
    /** The pause / stop reports, sent in order: a teardown fires a 'paused'
     *  and a 'stopped' a few milliseconds apart, and a 'paused' landing
     *  second put the item back in the server's Now Playing. */
    private val terminalReports = ReportLane(terminalScope)
    private var itemId: String? = null
    private var hlsOffsetMs: Long = 0
    /** The last pause / stop report sent, as (state, content position).
     *  A teardown fires several at once (the player's own pause callback,
     *  the fragment's onPause, then onStop), which sent the same final
     *  position two or three times within milliseconds. */
    private var lastTerminal: Pair<String, Long>? = null

    /** Position provider — returns the raw player position in ms. */
    var positionProvider: (() -> Long)? = null

    /** Duration provider — returns the total duration in ms. */
    var durationProvider: (() -> Long)? = null

    /** Fires (on the main thread) when the server refuses a 'playing'
     *  heartbeat with ANY 403 — the server no longer lets this profile watch
     *  the item. The tracker stops itself first; the caller tears playback
     *  down and shows the message for [sentinel], which uses the fragment's
     *  error-dialog vocabulary:
     *   - `watch_limit:<reason>` — PARENTAL_LIMIT: daily cap reached or the
     *     allowed-hours window closed mid-session.
     *   - `playback_stopped:<sentence>` — PLAYBACK_STOPPED: an admin stopped
     *     this stream (see PlaybackStop).
     *   - [CONTENT_REVOKED] — anything else: library access revoked or the
     *     content-rating ceiling lowered while this was playing. */
    var onBlocked: ((sentinel: String) -> Unit)? = null

    /** Point the tracker at [itemId] without starting the heartbeat, so
     *  pause / stop reports have an item before playback first starts. */
    fun bind(itemId: String, hlsOffsetMs: Long = 0) {
        this.itemId = itemId
        this.hlsOffsetMs = hlsOffsetMs
    }

    fun start(itemId: String, hlsOffsetMs: Long = 0) {
        this.itemId = itemId
        this.hlsOffsetMs = hlsOffsetMs
        lastTerminal = null
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
        if (repeatsLastTerminal("paused", snap)) return
        // Sent on the survivable scope: onPause often coincides with view
        // teardown, and the final position must persist even though the view
        // scope is being cancelled.
        terminalReports.launch { report("paused", snap) }
    }

    fun onStop() {
        job?.cancel()
        val snap = snapshot() ?: return
        if (repeatsLastTerminal("stopped", snap)) return
        terminalReports.launch { report("stopped", snap) }
    }

    /** Whether [state] at [snap]'s position was the last terminal report
     *  (so it would only repeat it); otherwise records it as the last one.
     *  Main thread, like the callers. */
    private fun repeatsLastTerminal(state: String, snap: Pair<Long, Long>): Boolean {
        val key = state to contentPosition(snap)
        if (lastTerminal == key) return true
        lastTerminal = key
        return false
    }

    fun stop() {
        job?.cancel()
        job = null
    }

    fun updateOffset(offsetMs: Long) {
        this.hlsOffsetMs = offsetMs
    }

    /**
     * One immediate 'playing' heartbeat, sent when the stream died under the
     * player (an HLS 403/404 — what a server-side stop looks like from
     * ExoPlayer). By then the player has paused this tracker, so the periodic
     * heartbeat — whose 403 is the backstop for a missed `playback.stop` SSE
     * event — would never run again. Returns the [onBlocked]-vocabulary
     * sentinel when the server refused the beat (and stops the tracker), else
     * null: accepted, or failed for any other reason. Call on the main thread
     * (it reads the player); [onBlocked] is NOT fired — the caller acts on the
     * return value.
     */
    suspend fun probeRefusal(): String? {
        val id = itemId ?: return null
        val snap = snapshot() ?: return null
        val dur = snap.second
        if (dur <= 0) return null
        val refusal = HeartbeatRefusal.heartbeat {
            itemRepo.updateProgress(id, contentPosition(snap), dur, "playing")
        } ?: return null
        stop()
        return blockSentinel(refusal)
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

    /** The content position of [snap], at most its duration: a VBR file
     *  can play a few seconds past the length it listed. */
    private fun contentPosition(snap: Pair<Long, Long>): Long {
        val (rawPos, dur) = snap
        val pos = rawPos + hlsOffsetMs
        return if (dur > 0) pos.coerceAtMost(dur) else pos
    }

    private suspend fun report(state: String, snapshot: Pair<Long, Long>) {
        val id = itemId ?: return
        val dur = snapshot.second
        if (dur <= 0) return

        val contentPos = contentPosition(snapshot)
        try {
            itemRepo.updateProgress(id, contentPos, dur, state)
            lastReportedContentMs = contentPos
        } catch (e: Exception) {
            // ANY 403 on a 'playing' heartbeat stops playback, not only a
            // PARENTAL_LIMIT one — see HeartbeatRefusal, which the background
            // OnScreenMediaSessionService shares. Mirrors the phone client's
            // PlayerViewModel.reportProgress. Other failures stay best-effort
            // (don't crash playback on a hiccup).
            val refusal = HeartbeatRefusal.of(state, e)
            if (refusal != null) {
                val sentinel = blockSentinel(refusal)
                // Dispatch the callback on terminalScope, NOT from this
                // coroutine: this code runs inside the heartbeat job that
                // stop() is about to cancel, and withContext() begins with
                // ensureActive() — so dispatching from here after stop()
                // threw CancellationException before the callback ever ran,
                // and the block screen never appeared. terminalScope
                // outlives the job by design.
                val cb = onBlocked
                if (cb != null) {
                    terminalScope.launch(Dispatchers.Main) { cb(sentinel) }
                }
                stop()
            }
        }
    }

    companion object {
        /** Error-dialog sentinel for a mid-session 403 that is NOT the parental
         *  watch limit. Distinct from the start path's `content_restricted`
         *  ("outside your content rating limit"): mid-session it may equally be
         *  a revoked library grant, so the message stays generic. */
        const val CONTENT_REVOKED = "content_revoked"

        /** Map a heartbeat refusal onto the fragment's error-dialog sentinel. */
        internal fun blockSentinel(refusal: HeartbeatRefusal): String = when (refusal) {
            is HeartbeatRefusal.WatchLimit -> "watch_limit:${refusal.reason ?: ""}"
            // Admin stop (403 PLAYBACK_STOPPED) — the same sentinel the SSE
            // playback.stop path produces, so both show one message.
            is HeartbeatRefusal.PlaybackStopped -> PlaybackStop.sentinel(refusal.message)
            HeartbeatRefusal.ContentRevoked -> CONTENT_REVOKED
        }
    }
}
