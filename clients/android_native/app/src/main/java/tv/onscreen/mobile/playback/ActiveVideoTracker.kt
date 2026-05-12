package tv.onscreen.mobile.playback

import java.util.concurrent.atomic.AtomicBoolean

/**
 * Process-wide flag MainActivity reads in onUserLeaveHint to decide
 * whether to auto-enter Picture-in-Picture when the user navigates
 * home with a video on screen.
 *
 * Why a singleton (not a Hilt-injected service / ViewModel state):
 * onUserLeaveHint fires on the activity callback thread BEFORE
 * Compose has a chance to recompose, so the value the activity
 * reads must be synchronous and set from anywhere in the tree.
 * AtomicBoolean keeps the read race-free without the ceremony of
 * a coroutine-scoped state holder.
 *
 * PlayerScreen flips the flag on (DisposableEffect enter) when a
 * video is loaded and active, off when the screen is leaving the
 * composition. Audio-only playback leaves the flag false — backing
 * out of an audio item just ends playback, so no PiP and no
 * background-service bridge are needed.
 */
object ActiveVideoTracker {
    private val flag = AtomicBoolean(false)
    /** Called whenever the playing-flag flips. MainActivity registers
     *  a listener that re-invokes setPictureInPictureParams with
     *  setAutoEnterEnabled mirroring this flag, so the system can
     *  honour the gesture without an onUserLeaveHint() bridge. */
    @Volatile private var listener: ((Boolean) -> Unit)? = null

    fun set(playing: Boolean) {
        val prev = flag.getAndSet(playing)
        if (prev != playing) listener?.invoke(playing)
    }

    fun isPlaying(): Boolean = flag.get()

    fun setListener(fn: ((Boolean) -> Unit)?) {
        listener = fn
    }
}
