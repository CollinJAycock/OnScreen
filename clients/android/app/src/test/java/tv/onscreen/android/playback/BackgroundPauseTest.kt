package tv.onscreen.android.playback

import androidx.media3.common.Player
import com.google.common.truth.Truth.assertThat
import org.junit.Test

class BackgroundPauseTest {

    private val now = 1_000_000L

    @Test
    fun `a paused player holds the foreground for the window, then lets go`() {
        assertThat(BackgroundPause.holdsForeground(false, Player.STATE_READY, now - 60_000L, now)).isTrue()
        assertThat(BackgroundPause.holdsForeground(false, Player.STATE_BUFFERING, now, now)).isTrue()
        assertThat(BackgroundPause.holdsForeground(false, Player.STATE_READY, now - BackgroundPause.HOLD_MS, now)).isFalse()
    }

    @Test
    fun `a pause not recorded yet still holds`() {
        // Media3 can ask before the service's listener has seen the pause.
        assertThat(BackgroundPause.holdsForeground(false, Player.STATE_READY, null, now)).isTrue()
    }

    @Test
    fun `playing, finished or idle players don't hold`() {
        assertThat(BackgroundPause.holdsForeground(true, Player.STATE_READY, null, now)).isFalse()
        assertThat(BackgroundPause.holdsForeground(false, Player.STATE_ENDED, null, now)).isFalse()
        assertThat(BackgroundPause.holdsForeground(false, Player.STATE_IDLE, null, now)).isFalse()
    }
}
