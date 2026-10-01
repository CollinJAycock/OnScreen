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
 * beginning" finally works on a resumed transcode. Past the end of a window
 * that has stopped growing there is nothing to re-issue: that clamps.
 *
 * The adapter seeks with seekTo(mediaItemIndex, positionMs) (its scrub) and
 * seekToDefaultPosition() (Play at the end); both are here. Relative seeks
 * (seekBack/seekForward) and Previous/Next are left on the raw delegate —
 * nothing on the glue's path calls them, and a ±10 s step is
 * offset-invariant.
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
    private val reloadStopped: () -> Boolean,
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

    /**
     * Past the end of a FINISHED window (a session written to its end, or
     * direct play) there is nothing further to transcode: the target clamps
     * to the end, which ends the item (and puts Up Next up) as before. The
     * last scrub step lands on the item's listed length, often a little past
     * the stream's own (the playlist's segment lengths), and re-issued there
     * it started a transcode at the end of the file: an empty playlist, an
     * error dialog.
     */
    override fun seekTo(positionMs: Long) {
        val off = offsetMs()
        val windowMs = super.getDuration()
        val windowEnd = if (windowMs == C.TIME_UNSET) Long.MAX_VALUE else off + windowMs
        if (positionMs > windowEnd && !isCurrentMediaItemDynamic) {
            super.seekTo(windowMs)
            return
        }
        if (positionMs < off || positionMs > windowEnd) {
            onSeekOutsideWindow(positionMs.coerceAtLeast(0L))
            return
        }
        super.seekTo(positionMs - off)
    }

    /** The transport bar's scrub: LeanbackPlayerAdapter.seekTo passes the
     *  current item's index with the position. Passed straight through, it
     *  took content time for session time — a scrub to 50:00 on a session
     *  resumed at 45:00 went for 95:00, or the live edge short of it. */
    override fun seekTo(mediaItemIndex: Int, positionMs: Long) {
        if (mediaItemIndex == currentMediaItemIndex) seekTo(positionMs) else super.seekTo(mediaItemIndex, positionMs)
    }

    /**
     * Play on the transport bar once an error has stopped the player
     * (Util.handlePlayButtonAction prepares an idle one first). Prepared
     * again on its old source, a server session still being written could
     * start away from where it stopped: MaskingMediaSource takes a position
     * equal to the old window's default for "none asked for" and moves it to
     * the grown window's, which trails its end by the first playlist's
     * length (TranscodeHls.mediaItem) — an error before the playlist first
     * grew, then Play, started as far in as it had grown since.
     * [reloadStopped] loads the session on a new source where the player
     * stopped instead, and says whether it did; direct play prepares again
     * as it is.
     */
    override fun prepare() {
        if (playbackState == Player.STATE_IDLE && reloadStopped()) return
        super.prepare()
    }

    /**
     * The default position is the item's start, as for a direct play: Play
     * on the transport bar once the item has ended (Util.handlePlayButtonAction,
     * which seeks to the default position) replays it from 0:00. Left to the
     * player, a server session's default is its own head, the resume point
     * of a resumed one, and while the session is still being written, a
     * point the length of the first playlist the player loaded behind its
     * live edge (TranscodeHls.mediaItem). 0:00 before the window re-issues
     * the session there, as a scrub to it does.
     */
    override fun seekToDefaultPosition() = seekTo(0L)

    override fun seekToDefaultPosition(mediaItemIndex: Int) {
        if (mediaItemIndex == currentMediaItemIndex) seekTo(0L) else super.seekToDefaultPosition(mediaItemIndex)
    }
}
