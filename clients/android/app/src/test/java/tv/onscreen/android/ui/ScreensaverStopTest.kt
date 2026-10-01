package tv.onscreen.android.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class ScreensaverStopTest {

    // elapsedRealtime-style clock: the screensaver came on at t.
    private val t = 5_000_000L

    @Test
    fun `a stop with no dream at all resets`() {
        // HOME, the launcher, a TV power-off: no DREAMING_STARTED ever seen.
        assertThat(ScreensaverStop.returnsToScreen(t, null, null, t + 60_000L)).isFalse()
    }

    @Test
    fun `the stop the dream caused returns, whichever arrived first`() {
        // Broadcast before the stop (as on the Fire TV Stick: ~1 s apart).
        assertThat(ScreensaverStop.returnsToScreen(t + 1_500L, t, null, t + 600_000L)).isTrue()
        // Stop before the broadcast was delivered.
        assertThat(ScreensaverStop.returnsToScreen(t - 2_000L, t, null, t + 600_000L)).isTrue()
        // Stop held back to the system's idle timeout by an animated dream.
        assertThat(ScreensaverStop.returnsToScreen(t + 10_500L, t, null, t + 600_000L)).isTrue()
    }

    @Test
    fun `a wake that beats the DREAMING_STOPPED broadcast still returns`() {
        // The last end seen is an older dream's: this one's hasn't landed yet.
        assertThat(ScreensaverStop.returnsToScreen(t + 1_000L, t, t - 3_600_000L, t + 7_200_000L)).isTrue()
    }

    @Test
    fun `waking straight back into the app returns`() {
        val woke = t + 1_800_000L
        assertThat(ScreensaverStop.returnsToScreen(t + 1_000L, t, woke, woke + 800L)).isTrue()
        assertThat(ScreensaverStop.returnsToScreen(t + 1_000L, t, woke, woke + ScreensaverStop.WAKE_WINDOW_MS)).isTrue()
    }

    @Test
    fun `waking to the launcher and coming back later resets`() {
        // HOME woke the dream; the user opened the app a minute later.
        val woke = t + 1_800_000L
        assertThat(ScreensaverStop.returnsToScreen(t + 1_000L, t, woke, woke + 60_000L)).isFalse()
        assertThat(
            ScreensaverStop.returnsToScreen(t + 1_000L, t, woke, woke + ScreensaverStop.WAKE_WINDOW_MS + 1L),
        ).isFalse()
    }

    @Test
    fun `a stop outside the dream is not the dream's`() {
        // HOME at t - 5 min; the screensaver later came on over the launcher.
        assertThat(ScreensaverStop.returnsToScreen(t - 300_000L, t, null, t + 900_000L)).isFalse()
        // A screensaver earlier in the session, woken; HOME much later.
        assertThat(ScreensaverStop.returnsToScreen(t + 900_000L, t, t + 600_000L, t + 900_500L)).isFalse()
        // The edge of the broadcast's lag.
        assertThat(
            ScreensaverStop.returnsToScreen(t - ScreensaverStop.BROADCAST_LAG_MS, t, null, t + 60_000L),
        ).isTrue()
        assertThat(
            ScreensaverStop.returnsToScreen(t - ScreensaverStop.BROADCAST_LAG_MS - 1L, t, null, t + 60_000L),
        ).isFalse()
    }
}
