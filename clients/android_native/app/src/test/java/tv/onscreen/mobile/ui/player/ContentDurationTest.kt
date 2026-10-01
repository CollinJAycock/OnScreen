package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class ContentDurationTest {

    private val timeUnset = Long.MIN_VALUE + 1 // androidx.media3.common.C.TIME_UNSET

    @Test
    fun `a trusted player's duration comes first`() {
        // Direct play: the player's timeline IS the file's, and the position
        // reported is in it.
        assertThat(ContentDuration.of(7_000_000L, 7_200_000L, 7_210_000L, playerDurationTrusted = true))
            .isEqualTo(7_210_000L)
    }

    @Test
    fun `then the file's probed duration, over the item's listed runtime`() {
        // An episode listed at 42 min whose file runs 45: against the runtime,
        // Up Next rose — and the episode read as watched — minutes early.
        assertThat(ContentDuration.of(2_520_000L, 2_700_000L, 240_000L, playerDurationTrusted = false))
            .isEqualTo(2_700_000L)
        // The API omits the item's duration for many items; the file has one.
        assertThat(ContentDuration.of(null, 7_200_000L, 240_000L, playerDurationTrusted = false))
            .isEqualTo(7_200_000L)
    }

    @Test
    fun `the item's runtime only when the file has none`() {
        assertThat(ContentDuration.of(7_000_000L, null, 240_000L, playerDurationTrusted = false))
            .isEqualTo(7_000_000L)
        assertThat(ContentDuration.of(7_000_000L, 0L, timeUnset, playerDurationTrusted = true))
            .isEqualTo(7_000_000L)
    }

    @Test
    fun `the player's only when trusted`() {
        assertThat(ContentDuration.of(null, null, 5_400_000L, playerDurationTrusted = true))
            .isEqualTo(5_400_000L)
        // The device bug: an HLS window of the ~4 minutes produced so far.
        assertThat(ContentDuration.of(null, null, 240_000L, playerDurationTrusted = false))
            .isEqualTo(ContentDuration.UNKNOWN)
        assertThat(ContentDuration.of(null, 7_200_000L, 240_000L, playerDurationTrusted = false))
            .isEqualTo(7_200_000L)
    }

    @Test
    fun `nothing known is unknown, not a guess`() {
        assertThat(ContentDuration.of(null, null, timeUnset, playerDurationTrusted = true))
            .isEqualTo(ContentDuration.UNKNOWN)
        assertThat(ContentDuration.of(null, 0L, 0L, playerDurationTrusted = true))
            .isEqualTo(ContentDuration.UNKNOWN)
    }

    @Test
    fun `an HLS session's player duration is never trusted`() {
        assertThat(ContentDuration.playerDurationTrusted(hlsSession = true, windowDynamic = false, windowLive = false))
            .isFalse()
        // Even once the playlist has ended and the window settled: a resumed
        // session still covers only the rest of the file.
        assertThat(ContentDuration.playerDurationTrusted(hlsSession = true, windowDynamic = false, windowLive = true))
            .isFalse()
    }

    @Test
    fun `a dynamic or live window is never trusted`() {
        assertThat(ContentDuration.playerDurationTrusted(hlsSession = false, windowDynamic = true, windowLive = false))
            .isFalse()
        assertThat(ContentDuration.playerDurationTrusted(hlsSession = false, windowDynamic = false, windowLive = true))
            .isFalse()
    }

    @Test
    fun `a settled progressive window is trusted`() {
        assertThat(ContentDuration.playerDurationTrusted(hlsSession = false, windowDynamic = false, windowLive = false))
            .isTrue()
    }
}
