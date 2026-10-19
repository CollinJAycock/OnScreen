package tv.onscreen.android.playback

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import tv.onscreen.android.data.api.HeartbeatRefusal

/**
 * The background service's 'playing' heartbeats ([OnScreenMediaSessionService]).
 *
 * The service's beat loop used to await each PUT before waiting for the next
 * tick, so a PUT that hung (a 42 s stall was seen on a Google TV; the API
 * client's read timeout is 60 s) held every later beat back with it. Now:
 *
 *  - a beat goes out on the report [lane], in order with the service's
 *    'paused' and 'stopped' reports (a 'playing' landing after the 'paused'
 *    that followed it put the item back in the server's Now Playing), and
 *    the loop never waits for it;
 *  - a beat still out when the next falls due is not doubled up: that tick
 *    is skipped ([beat] returns false);
 *  - a beat is given [timeoutMs] and then cancelled, which cancels its HTTP
 *    call, so a hung one frees the lane well before the client's timeout;
 *  - a pause or stop report about to go on the lane gives a beat still out
 *    [graceMs] more ([beforeTerminal]), then cancels it, as the player
 *    screen's ProgressTracker does, so the final position isn't held up.
 *
 * Main thread, like the service.
 */
class BackgroundBeats(
    private val lane: ReportLane,
    /** Runs the grace-period cancel; outlives any one service instance. */
    private val scope: CoroutineScope,
    private val timeoutMs: Long = BEAT_TIMEOUT_MS,
    private val graceMs: Long = BEAT_GRACE_MS,
) {
    private var inFlight: Job? = null

    /** Whether a beat is still out (queued or sending). */
    val busy: Boolean get() = inFlight?.isActive == true

    /**
     * Queue one beat: [send] is its PUT. [onRefused] runs, on the lane's
     * thread, when the server refused it (see [HeartbeatRefusal]); any other
     * failure, and a timeout, stay best-effort. Returns false, sending
     * nothing, while the last beat is still out.
     */
    fun beat(send: suspend () -> Unit, onRefused: (HeartbeatRefusal) -> Unit): Boolean {
        if (busy) return false
        inFlight = lane.launch {
            val refusal = withTimeoutOrNull(timeoutMs) { HeartbeatRefusal.heartbeat(send) }
            if (refusal != null) onRefused(refusal)
        }
        return true
    }

    /** A pause / stop report is about to be queued: cancel a beat still out
     *  once [graceMs] has passed (an ordinary round trip still lands first). */
    fun beforeTerminal() {
        val beat = inFlight?.takeIf { it.isActive } ?: return
        scope.launch {
            delay(graceMs)
            beat.cancel()
        }
    }

    companion object {
        /** Longer than an ordinary round trip, shorter than the 10 s beat
         *  interval plus one: a hung beat costs at most one skipped tick. */
        const val BEAT_TIMEOUT_MS = 15_000L

        /** As ProgressTracker.BEAT_GRACE_MS. */
        const val BEAT_GRACE_MS = 2_000L
    }
}
