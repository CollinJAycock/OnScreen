package tv.onscreen.mobile.playback

import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.LoadControl
import androidx.media3.exoplayer.analytics.PlayerId
import androidx.media3.exoplayer.source.MediaSource
import androidx.media3.exoplayer.source.SinglePeriodTimeline
import com.google.common.truth.Truth.assertThat
import org.junit.Test

@androidx.annotation.OptIn(UnstableApi::class)
class BufferProfileTest {

    private val playerId = PlayerId("test")

    // A streamed item: no local URI, so the load control applies its streaming
    // thresholds (a server stream: direct play over HTTP, remux or transcode).
    private val timeline = SinglePeriodTimeline(C.TIME_UNSET, true, false, false, null, MediaItem.EMPTY)
    private val period = MediaSource.MediaPeriodId(timeline.getUidOfPeriod(0))

    private fun control(): LoadControl = BufferProfile.loadControl().also { it.onPrepared(playerId) }

    /** Whether [this] starts (or resumes) playback with [bufferedMs] buffered. */
    private fun LoadControl.startsWith(bufferedMs: Long, afterRebuffer: Boolean): Boolean =
        shouldStartPlayback(
            LoadControl.Parameters(
                playerId,
                timeline,
                period,
                /* playbackPositionUs= */ 0L,
                /* bufferedDurationUs= */ bufferedMs * 1_000L,
                /* playbackSpeed= */ 1f,
                /* playWhenReady= */ true,
                /* rebuffering= */ afterRebuffer,
                /* targetLiveOffsetUs= */ C.TIME_UNSET,
                /* lastRebufferRealtimeMs= */ C.TIME_UNSET,
            ),
        )

    @Test
    fun `playback waits for Media3 1_3_1's thresholds, not 1_9's`() {
        val lc = control()
        // Media3 1.9+ starts at 1 s; 1.3.1 waited for 2.5 s.
        assertThat(lc.startsWith(1_000L, afterRebuffer = false)).isFalse()
        assertThat(lc.startsWith(2_500L, afterRebuffer = false)).isTrue()
        // A remux stall: ~4 s buffered restarted playback under 1.9's 2 s and
        // stalled again at once. 1.3.1's 5 s holds it until it can run.
        assertThat(lc.startsWith(4_000L, afterRebuffer = true)).isFalse()
        assertThat(lc.startsWith(5_000L, afterRebuffer = true)).isTrue()
    }
}
