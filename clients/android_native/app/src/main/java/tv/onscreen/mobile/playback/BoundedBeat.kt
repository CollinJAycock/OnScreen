package tv.onscreen.mobile.playback

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import tv.onscreen.mobile.data.api.HeartbeatRefusal

/**
 * The background service's 'playing' heartbeats ([PlaybackService]), sent
 * without holding up the beat loop. The loop used to await each PUT, so one
 * that hung (the API client's read timeout is 60 s) held every later beat
 * back. Now a beat runs on its own job on [scope], is cancelled (its HTTP call
 * with it) after [timeoutMs], and a tick that falls due while the last beat is
 * still out is skipped rather than doubled up. Same rules as the Android TV
 * app's BackgroundBeats.
 *
 * Main thread, like the service.
 */
class BoundedBeat(
    private val scope: CoroutineScope,
    private val timeoutMs: Long = BEAT_TIMEOUT_MS,
) {
    private var inFlight: Job? = null

    /** Whether a beat is still out. */
    val busy: Boolean get() = inFlight?.isActive == true

    /** Send one beat ([send] is its PUT) unless the last is still out (then
     *  false). [onRefused] runs on [scope] when the server refused it; any
     *  other failure, and a timeout, stay best-effort. */
    fun beat(send: suspend () -> Unit, onRefused: (HeartbeatRefusal) -> Unit): Boolean {
        if (busy) return false
        inFlight = scope.launch {
            val refusal = withTimeoutOrNull(timeoutMs) { HeartbeatRefusal.heartbeat(send) }
            if (refusal != null) onRefused(refusal)
        }
        return true
    }

    fun cancel() {
        inFlight?.cancel()
        inFlight = null
    }

    companion object {
        /** Longer than an ordinary round trip, shorter than the 10 s beat
         *  interval plus one: a hung beat costs at most one skipped tick. */
        const val BEAT_TIMEOUT_MS = 15_000L
    }
}
