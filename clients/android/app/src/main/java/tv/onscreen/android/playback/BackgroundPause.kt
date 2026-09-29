package tv.onscreen.android.playback

import androidx.media3.common.Player

/**
 * A paused player in the background service. Media3 takes the service out
 * of the foreground when playback pauses, and Android then stops a
 * background service about a minute later ("app idle"), releasing the
 * player: a book paused from the remote could no longer be resumed. The
 * service stays in the foreground for [HOLD_MS] after a pause instead,
 * then releases the player itself. Pure, JVM-tested in BackgroundPauseTest.
 */
object BackgroundPause {

    /** How long a paused background player stays resumable. */
    const val HOLD_MS = 10 * 60_000L

    /**
     * Whether the service stays in the foreground for its player: paused
     * (not ended, not idle, not playing) and within [HOLD_MS] of the pause.
     * [pausedAtMs] is null when the pause has just happened and the
     * service's own listener hasn't recorded it yet (Media3 may ask first).
     */
    fun holdsForeground(playWhenReady: Boolean, playbackState: Int, pausedAtMs: Long?, nowMs: Long): Boolean {
        if (playWhenReady) return false
        if (playbackState != Player.STATE_READY && playbackState != Player.STATE_BUFFERING) return false
        return pausedAtMs == null || nowMs - pausedAtMs < HOLD_MS
    }
}
