package tv.onscreen.android.ui.playback

import androidx.media3.common.Player
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
    // Scope for the reports themselves: the terminal pause/stop ones, and
    // the heartbeat's PUTs (see [start]), unless [reports] brings a lane
    // of its own; and for the beat's grace cancel and a refusal's callback
    // (see sendTerminal, report). The injected [scope] is the
    // fragment's viewLifecycleOwner.lifecycleScope, cancelled the instant the
    // view is destroyed — so a `paused`/`stopped` report launched there during
    // teardown (onStop → onDestroyView) could be cancelled before the PUT leaves
    // the device, losing the final position. This default outlives the view so
    // the last position persists (SupervisorJob so one failed report doesn't
    // poison the next). Injectable so tests can drive it with a test dispatcher.
    private val terminalScope: CoroutineScope =
        CoroutineScope(SupervisorJob() + Dispatchers.IO),
    /** Monotonic ms for the heartbeat's phase (see [nextBeatAtMs]).
     *  Injectable so tests can run it on virtual time. */
    private val nowMs: () -> Long = { android.os.SystemClock.elapsedRealtime() },
    /** Where its reports go out, in order with those of the trackers
     *  before and after it on the same screen: see [Reports]. */
    private val reports: Reports = Reports(terminalScope),
) {
    private var job: Job? = null
    /** When the next 'playing' heartbeat is due ([nowMs] time), kept across
     *  a hold ([stop]) so a rebuffer only delays a beat instead of restarting
     *  the 10 s count: a stall that flapped buffering / ready restarted it on
     *  every flap and sent no heartbeat at all for as long as it lasted.
     *  Null once a pause or stop report ends the phase. */
    private var nextBeatAtMs: Long? = null
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

    /** Run the 10 s heartbeat for [itemId]. Already running for it: the
     *  phase stands. After a hold ([stop]) it picks the phase up again, with
     *  a beat at once when one fell due during the hold.
     *
     *  A beat's PUT goes out on the report lane, not in this job: a hold
     *  cancels the job, and with the PUT inside it, a stall whose ready
     *  spells were shorter than the server's round trip cancelled every
     *  beat in flight, so none landed for as long as it lasted. A beat
     *  that falls due while the last one is still in flight (this
     *  tracker's, or one it replaced: [Reports]) is skipped. */
    fun start(itemId: String, hlsOffsetMs: Long = 0) {
        val sameItem = itemId == this.itemId
        this.itemId = itemId
        this.hlsOffsetMs = hlsOffsetMs
        lastTerminal = null
        if (sameItem && job?.isActive == true) return
        job?.cancel()
        val now = nowMs()
        val firstMs = nextBeatAtMs?.takeIf { sameItem }?.let { (it - now).coerceAtLeast(0L) } ?: HEARTBEAT_MS
        nextBeatAtMs = now + firstMs
        job = scope.launch {
            var waitMs = firstMs
            while (isActive) {
                delay(waitMs)
                waitMs = HEARTBEAT_MS
                // Sent (below) or skipped, this beat is done with: nothing
                // left for a hold to cancel.
                nextBeatAtMs = nowMs() + HEARTBEAT_MS
                if (reports.beatInFlight?.isActive == true) continue
                // The heartbeat runs on the (main) lifecycle scope, so reading
                // the player via snapshot() here is already on the right thread.
                val snap = snapshot() ?: continue
                reports.beatInFlight = send("playing", snap)
            }
        }
    }

    /** Apply the fragment's [heartbeatFor] decision for [itemId]. */
    fun follow(heartbeat: Heartbeat, itemId: String, hlsOffsetMs: Long) {
        when (heartbeat) {
            Heartbeat.START -> start(itemId, hlsOffsetMs)
            Heartbeat.HOLD -> stop()
            Heartbeat.PAUSE -> onPause()
            Heartbeat.NONE -> Unit
        }
    }

    fun onPause() {
        job?.cancel()
        nextBeatAtMs = null
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
        sendTerminal("paused", snap)
    }

    fun onStop() {
        job?.cancel()
        nextBeatAtMs = null
        val snap = snapshot() ?: return
        if (repeatsLastTerminal("stopped", snap)) return
        sendTerminal("stopped", snap)
    }

    /** Queue the terminal [state] report of [snap], behind a heartbeat still
     *  in flight for at most [BEAT_GRACE_MS]: an ordinary round trip, so the
     *  'playing' still lands first. One out longer than that is cancelled
     *  (the position it carries is stale by now anyway): a hung beat held a
     *  teardown's final 'paused' / 'stopped' behind it for as long as its
     *  call took to time out, 30 s. One still waiting its turn behind an
     *  earlier report then goes unsent, but holds its place: the reports
     *  behind it still wait for that earlier one (ReportLane). A hold
     *  ([stop]) leaves the beat be. The beat may be a replaced tracker's. */
    private fun sendTerminal(state: String, snap: Pair<Long, Long>) {
        send(state, snap) ?: return
        val beat = reports.beatInFlight?.takeIf { it.isActive } ?: return
        terminalScope.launch {
            delay(BEAT_GRACE_MS)
            beat.cancel()
        }
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

    /** Stop the heartbeat without a report: a hold (a rebuffer) whose
     *  [start] picks the phase up again, or the tracker's retirement. */
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

    /** Queue a [state] report of [snapshot] on the report lane. What it
     *  reports (item, content position) is read here, on the main thread,
     *  when it is sent, not when its turn on the lane comes. Null when there
     *  is nothing to report: no item yet, or no known duration. */
    private fun send(state: String, snapshot: Pair<Long, Long>): Job? {
        val id = itemId ?: return null
        val dur = snapshot.second
        if (dur <= 0) return null
        val contentPos = contentPosition(snapshot)
        return reports.lane.launch { report(id, state, contentPos, dur) }
    }

    private suspend fun report(id: String, state: String, contentPos: Long, dur: Long) {
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
                // Stop, and tell the caller, on the main thread, where the
                // heartbeat and the fragment live: this runs on the report
                // lane (IO). A fresh launch rather than
                // withContext: the callback used to be dispatched from the
                // heartbeat job itself, which stop() cancels, and
                // withContext() begins with ensureActive(), so it threw
                // before the callback ever ran and the block screen never
                // appeared. terminalScope outlives the job by design.
                terminalScope.launch(Dispatchers.Main) {
                    stop()
                    onBlocked?.invoke(sentinel)
                }
            }
        }
    }

    /**
     * Where trackers send their reports: all of them, heartbeats too, in
     * order on one [ReportLane] (a teardown fires a 'paused' and a 'stopped'
     * a few milliseconds apart, and a 'paused' landing second put the item
     * back in the server's Now Playing; a 'playing' landing after the
     * 'paused' that followed it did the same), and the last heartbeat's PUT
     * while it is in flight, which a pause or stop waits for only so long
     * (see sendTerminal).
     *
     * The player screen keeps one for every tracker it installs, one after
     * another (a session re-issued, a player taken back from the background
     * service). With a lane each, a replaced tracker's beat still out was
     * outside both: its 'playing' could land after the new tracker's final
     * 'paused' or 'stopped', and a hung one was never cancelled.
     */
    class Reports(scope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)) {
        internal val lane = ReportLane(scope)
        /** The last heartbeat's PUT, whichever tracker sent it, while it is
         *  in flight. Main thread, like the trackers. */
        internal var beatInFlight: Job? = null
    }

    /** What the heartbeat does on a player change: see [heartbeatFor]. */
    enum class Heartbeat {
        /** Playing: run it ([start]). */
        START,
        /** Waiting to play: hold it, no report ([stop]). */
        HOLD,
        /** Paused: report 'paused' ([onPause]). */
        PAUSE,
        /** Nothing: the end of the item reports itself ([onStop]). */
        NONE,
    }

    companion object {
        /** Error-dialog sentinel for a mid-session 403 that is NOT the parental
         *  watch limit. Distinct from the start path's `content_restricted`
         *  ("outside your content rating limit"): mid-session it may equally be
         *  a revoked library grant, so the message stays generic. */
        const val CONTENT_REVOKED = "content_revoked"

        /** The heartbeat interval. */
        const val HEARTBEAT_MS = 10_000L

        /** How long a pause / stop report waits for a heartbeat still in
         *  flight before cancelling it (see sendTerminal). */
        const val BEAT_GRACE_MS = 2_000L

        /**
         * The heartbeat for a player that [isPlaying] or not, with
         * [playWhenReady] and [playbackState] as they now are. A rebuffer
         * (still meant to play, waiting for data: a thin buffer, a seek) is
         * not a pause: it holds the heartbeat without a 'paused' report. A
         * stall that flapped between buffering and ready sent a 'paused' PUT
         * per flap, five in four seconds, each putting the item in Now
         * Playing as paused. Nor is the display-switch hold
         * ([heldForFrameRate]), which pauses a player the user started.
         */
        fun heartbeatFor(
            isPlaying: Boolean,
            playWhenReady: Boolean,
            playbackState: Int,
            heldForFrameRate: Boolean,
        ): Heartbeat = when {
            isPlaying -> Heartbeat.START
            playbackState == Player.STATE_ENDED -> Heartbeat.NONE
            playWhenReady && playbackState == Player.STATE_BUFFERING -> Heartbeat.HOLD
            heldForFrameRate -> Heartbeat.HOLD
            // Paused (while buffering too), suppressed (another app took
            // the audio), or failed.
            else -> Heartbeat.PAUSE
        }

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
