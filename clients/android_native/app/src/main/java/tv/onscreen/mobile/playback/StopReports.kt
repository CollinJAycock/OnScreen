package tv.onscreen.mobile.playback

import androidx.media3.common.Player

/**
 * When the background player's queue moves off an item, whether that is the
 * item's terminal 'stopped' report (the server's resume + scrobble trigger).
 * Pure so the rules are unit-tested (StopReportsTest); PlaybackService applies
 * them in onPositionDiscontinuity.
 */
object StopReports {

    /**
     * A discontinuity that leaves [leftId] for [newId] reports [leftId]
     * stopped when:
     *  - it's a gapless hand-off (AUTO_TRANSITION — position = full length),
     *    a next / previous / queue jump (SEEK, SKIP), or the UI replacing the
     *    queue mid-track (REMOVE while the player is still active);
     *  - and it really changes item (a seek within a track, or a restart via
     *    "previous", is not a stop).
     *
     * A REMOVE from a STOPPED (IDLE) player never reports: every stop + clear
     * path — the mini player's ✕, a heartbeat refusal, an admin stop, sign-out
     * — calls stop() first and has already reported (or deliberately not)
     * through STATE_IDLE before the queue is cleared.
     */
    fun onDiscontinuity(reason: Int, playbackState: Int, leftId: String?, newId: String?): Boolean {
        if (leftId.isNullOrEmpty() || leftId == newId) return false
        return when (reason) {
            Player.DISCONTINUITY_REASON_AUTO_TRANSITION,
            Player.DISCONTINUITY_REASON_SEEK,
            Player.DISCONTINUITY_REASON_SKIP,
            -> true
            Player.DISCONTINUITY_REASON_REMOVE -> playbackState != Player.STATE_IDLE
            else -> false
        }
    }
}

/**
 * One admin stop can arrive three ways for the same item — the SSE event, the
 * 403 on the next heartbeat, the 403 on the next media request — and should
 * stop (and toast) once.
 *
 * Scoped to the halted playback, not to a time window: PlaybackService calls
 * [reset] whenever an item becomes current, so the user playing the same
 * track again — even seconds later, still inside the server's stop window —
 * is a new playback, and its refusal halts it (queue cleared, message shown).
 * A 15 s window swallowed exactly that refusal, leaving the errored track in
 * the mini player with no explanation. Duplicates of the original stop can't
 * reach a new playback's queue: the halt clears the queue, and every path
 * reads the player's current item.
 */
class AdminStopDedupe {
    private var halted: String? = null

    /** True the first time [itemId] is stopped in the current playback. */
    fun first(itemId: String): Boolean {
        if (itemId == halted) return false
        halted = itemId
        return true
    }

    /** An item became current: the next stop, of any item, is a new one. */
    fun reset() {
        halted = null
    }
}

/**
 * Whether the background player should hold the `playback.stop` SSE
 * subscription: only while it is actually playing — or buffering to play — an
 * item. A paused, ended or failed player left in the background has nothing
 * streaming to stop, and holding the (shared, keepalive-every-30 s) socket for
 * it kept the radio waking for hours. Resuming re-subscribes; a stop that
 * lands while paused is still enforced by the 403 on the next media request
 * or heartbeat.
 */
fun wantsAdminStopEvents(playWhenReady: Boolean, playbackState: Int, hasItem: Boolean): Boolean =
    hasItem && playWhenReady &&
        (playbackState == Player.STATE_READY || playbackState == Player.STATE_BUFFERING)
