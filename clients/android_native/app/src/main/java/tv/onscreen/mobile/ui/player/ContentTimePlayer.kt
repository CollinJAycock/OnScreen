package tv.onscreen.mobile.ui.player

import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.Player
import androidx.media3.common.Timeline
import androidx.media3.common.util.Util
import androidx.media3.exoplayer.source.ForwardingTimeline

/**
 * What the PlayerView's own controls see of a remux / transcode session: the
 * content's clock instead of the session's.
 *
 * A session starts where the viewer resumes ([offsetMs] into the content) and
 * its playlist grows as the server writes it, so its player counts from 0 and
 * knows only the length written so far. The stock controller showed a film
 * resumed halfway as "0:00 of a few minutes, growing". Here the position and
 * buffered position are shifted by the offset and the duration (the
 * timeline's window too, which is what the time bar reads) is the content's
 * length ([contentLengthMs]), so the bar reads like the file's.
 *
 * Seeks come back in content time: inside the session they go to [inner] in
 * its own time (where [HlsSessionPlayer] restarts the session for a point it
 * hasn't written yet); before the session's start, [seekOutside] starts a new
 * session there.
 *
 * Only the PlayerView gets this: the rest of the screen drives [inner] and
 * adds the offset itself.
 */
internal class ContentTimePlayer(
    private val inner: Player,
    private val offsetMs: () -> Long,
    private val contentLengthMs: () -> Long,
    private val seekOutside: (Long) -> Boolean,
) : ForwardingPlayer(inner) {

    override fun getCurrentPosition(): Long = inner.currentPosition + offsetMs()

    override fun getContentPosition(): Long = inner.contentPosition + offsetMs()

    override fun getBufferedPosition(): Long = inner.bufferedPosition + offsetMs()

    override fun getContentBufferedPosition(): Long = inner.contentBufferedPosition + offsetMs()

    override fun getDuration(): Long = contentLengthMs().takeIf { it > 0 } ?: inner.duration

    override fun getContentDuration(): Long = contentLengthMs().takeIf { it > 0 } ?: inner.contentDuration

    override fun getCurrentTimeline(): Timeline {
        val timeline = inner.currentTimeline
        val lengthMs = contentLengthMs()
        if (timeline.isEmpty || lengthMs <= 0) return timeline
        return object : ForwardingTimeline(timeline) {
            override fun getWindow(windowIndex: Int, window: Timeline.Window, defaultPositionProjectionUs: Long): Timeline.Window {
                super.getWindow(windowIndex, window, defaultPositionProjectionUs)
                window.durationUs = Util.msToUs(lengthMs)
                return window
            }
        }
    }

    override fun seekTo(positionMs: Long) = seekTo(currentMediaItemIndex, positionMs)

    override fun seekTo(mediaItemIndex: Int, positionMs: Long) {
        val sessionMs = positionMs - offsetMs()
        if (sessionMs < 0 && seekOutside(positionMs)) return
        inner.seekTo(mediaItemIndex, sessionMs.coerceAtLeast(0L))
    }
}
