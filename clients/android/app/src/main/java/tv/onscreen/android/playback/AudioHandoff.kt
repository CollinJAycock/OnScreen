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

    /** Snapshot of the parked item so the service can address
     *  progress reports + auto-advance lookups without re-fetching
     *  the item over the network. Captured at park time on the
     *  fragment side from the same state the player is bound to. */
    data class Metadata(
        val itemId: String,
        val itemType: String,
        val parentId: String?,
        val index: Int?,
        val hlsOffsetMs: Long,
        /** The item's authoritative duration, carried so the service's
         *  progress reporter can absolutise like the fragment does.
         *  A resumed HLS session's player.duration is only the REMAINING
         *  time, so pairing it with an offset-corrected position reported
         *  e.g. 3 h against a 5 h-remaining duration — crossing the
         *  server's watched threshold hours early. Null when unknown. */
        val itemDurationMs: Long? = null,
    )

    private var parked: ExoPlayer? = null
    private var parkedMeta: Metadata? = null

    /** Park a player for pickup by the MediaSessionService. */
    @Synchronized
    fun park(player: ExoPlayer, meta: Metadata) {
        parked?.takeIf { it !== player }?.release()
        parked = player
        parkedMeta = meta
    }

    /** Take the parked player out of the slot. Returns null if none
     *  is currently parked, or if [forItemId] doesn't match — the
     *  caller is asking for a specific item and the parked one is
     *  for something else, so it should keep playing in the service
     *  while the new fragment builds its own player. */
    @Synchronized
    fun take(forItemId: String): ExoPlayer? {
        if (parkedMeta?.itemId != forItemId) return null
        val p = parked
        parked = null
        parkedMeta = null
        return p
    }

    /** Refresh the metadata for the already-parked player. The service
     *  calls this when it auto-advances to a new track in the
     *  background, so a fragment reclaiming the player afterward binds
     *  to the track that's actually playing rather than the one
     *  originally parked. No-op when nothing is parked. */
    @Synchronized
    fun updateMetadata(meta: Metadata) {
        if (parked != null) parkedMeta = meta
    }

    /** Inspection without removal — used by the service to decide
     *  whether to bind a session on first start. */
    @Synchronized
    fun peek(): ExoPlayer? = parked

    /** Metadata snapshot that goes alongside the parked player.
     *  Read by the service immediately after attach() so the
     *  progress reporter knows which item to PUT against and the
     *  auto-advance listener can compute the next sibling. */
    @Synchronized
    fun peekMetadata(): Metadata? = parkedMeta

    /** Unconditional clear — used by the service when it's about
     *  to release the player itself (playback ended, user dismissed
     *  notification). */
    @Synchronized
    fun clear() {
        parked = null
        parkedMeta = null
    }

    /** Stop and release any parked background audio and stop the session
     *  service. Used on every sign-out (voluntary and involuntary): a parked
     *  player otherwise keeps streaming on the signed-out user's already-issued
     *  stream token, and its MediaSession keeps advertising their track.
     *  Release the parked player first so the service's own teardown has
     *  nothing left to hand back, then stop the service. Idempotent.
     *  Must be called on the main thread (ExoPlayer is main-thread bound). */
    @androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
    fun stopAll(context: android.content.Context) {
        peek()?.let { player ->
            runCatching {
                player.stop()
                player.release()
            }
        }
        clear()
        runCatching {
            context.stopService(
                android.content.Intent(context, OnScreenMediaSessionService::class.java),
            )
        }
    }
}
