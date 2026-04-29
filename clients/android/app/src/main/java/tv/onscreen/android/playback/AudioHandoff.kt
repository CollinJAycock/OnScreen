package tv.onscreen.android.playback

import androidx.media3.exoplayer.ExoPlayer

/**
 * Process-wide handoff slot for an ExoPlayer that's transitioning
 * between PlaybackFragment ownership and the MediaSessionService.
 *
 * Why a singleton rather than a proper bound-service binder: Media3's
 * MediaSessionService only exposes a SessionToken-based MediaController
 * connection, not the raw player. To hand a fragment-built player to
 * the service we need a side channel, and a tightly-scoped object
 * with three methods is cheaper than threading the ExoPlayer through
 * Hilt as a Singleton (which would force every PlaybackFragment to
 * share one player instance even when there's no audio in flight).
 *
 * The slot holds zero or one player. Parking a second player while
 * one is already parked releases the old one — the user who starts
 * a fresh track expects the previous one to stop, not pile up.
 */
object AudioHandoff {

    private var parked: ExoPlayer? = null
    private var parkedItemId: String? = null

    /** Park a player for pickup by the MediaSessionService. */
    @Synchronized
    fun park(player: ExoPlayer, itemId: String) {
        parked?.takeIf { it !== player }?.release()
        parked = player
        parkedItemId = itemId
    }

    /** Take the parked player out of the slot. Returns null if none
     *  is currently parked, or if [forItemId] doesn't match — the
     *  caller is asking for a specific item and the parked one is
     *  for something else, so it should keep playing in the
     *  service while the new fragment builds its own player. */
    @Synchronized
    fun take(forItemId: String): ExoPlayer? {
        if (parkedItemId != forItemId) return null
        val p = parked
        parked = null
        parkedItemId = null
        return p
    }

    /** Inspection without removal — used by the service to decide
     *  whether to bind a session on first start. */
    @Synchronized
    fun peek(): ExoPlayer? = parked

    /** Unconditional clear — used by the service when it's about
     *  to release the player itself (playback ended, user dismissed
     *  notification). */
    @Synchronized
    fun clear() {
        parked = null
        parkedItemId = null
    }
}
