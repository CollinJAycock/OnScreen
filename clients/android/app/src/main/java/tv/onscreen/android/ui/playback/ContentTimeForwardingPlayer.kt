package tv.onscreen.android.ui.playback

import androidx.media3.common.C
import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi

/**
 * Content-time view of the player, for the Leanback transport bar ONLY.
 *
 * A transcode/HLS session started at a resume point begins its own timeline
 * at zero, so the glue used to render "0:00 / 1:15:00" when resuming a
 * 2-hour film at 45:00 — position looked like the start of the movie and
 * the runtime became the leftover 75 minutes. The old in-code NOTE declared
 * this unfixable because LeanbackPlayerAdapter and the glue's seek methods
 * are final — but the adapter reads position/duration/seek exclusively
 * through the [Player] interface, which is exactly the surface
 * [ForwardingPlayer] exists to decorate. The glue gets this wrapper; the
 * fragment, progress tracker, markers, media keys and the service handoff
 * keep the RAW player and their existing own-offset arithmetic.
 *
 * Absolute seeks are translated back to session time. A target OUTSIDE the
 * session's transcoded window — before the resume point, or past the
 * growing live edge — cannot be reached by an in-window seek at all (before
 * the window it doesn't exist; past the edge ExoPlayer clamps and
 * undershoots), so those route to [onSeekOutsideWindow], which re-issues
 * the session at the target. Side effect worth having: "scrub back to the
 * beginning" finally works on a resumed transcode.
 *
 * Relative seeks (seekBack/seekForward) are left on the raw delegate —
 * a ±10 s step is offset-invariant.
 */
// @UnstableApi, not @OptIn: this class EXTENDS ForwardingPlayer, and Kotlin
// will not let you opt in on behalf of a supertype — a class whose superclass
// requires opt-in must carry the marker itself and propagate it. The single
// construction site (PlaybackFragment) opts in locally.
@UnstableApi
class ContentTimeForwardingPlayer(
    player: Player,
    private val offsetMs: () -> Long,
    private val contentDurationMs: () -> Long,
    private val onSeekOutsideWindow: (contentPositionMs: Long) -> Unit,
) : ForwardingPlayer(player) {

    override fun getCurrentPosition(): Long = super.getCurrentPosition() + offsetMs()

    override fun getContentPosition(): Long = super.getContentPosition() + offsetMs()

    override fun getBufferedPosition(): Long = super.getBufferedPosition() + offsetMs()

    override fun getContentBufferedPosition(): Long =
        super.getContentBufferedPosition() + offsetMs()

    override fun getDuration(): Long {
        // The item's authoritative duration when known — the session
        // window's duration is only the (still growing) remainder.
        val known = contentDurationMs()
        if (known > 0) return known
        val d = super.getDuration()
        return if (d == C.TIME_UNSET) d else d + offsetMs()
    }

    override fun seekTo(positionMs: Long) {
        val off = offsetMs()
        val windowMs = super.getDuration()
        val windowEnd = if (windowMs == C.TIME_UNSET) Long.MAX_VALUE else off + windowMs
        if (positionMs < off || positionMs > windowEnd) {
            onSeekOutsideWindow(positionMs.coerceAtLeast(0L))
            return
        }
        super.seekTo(positionMs - off)
    }
}
