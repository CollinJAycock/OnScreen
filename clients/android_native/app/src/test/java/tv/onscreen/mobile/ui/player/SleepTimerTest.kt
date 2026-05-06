package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class SleepTimerTest {

    @Test
    fun `initialMs converts minutes to ms`() {
        assertThat(SleepTimerMath.initialMs(SleepTimer.Minutes(15))).isEqualTo(15L * 60L * 1000L)
        assertThat(SleepTimerMath.initialMs(SleepTimer.Minutes(60))).isEqualTo(60L * 60L * 1000L)
    }

    @Test
    fun `EndOfTrack and Off both yield zero initial ms`() {
        // EndOfTrack runs off track-position, not a wall-clock
        // countdown — initialMs is 0 by design. Off is the unset
        // state; the UI hides the chip for both.
        assertThat(SleepTimerMath.initialMs(SleepTimer.EndOfTrack)).isEqualTo(0)
        assertThat(SleepTimerMath.initialMs(SleepTimer.Off)).isEqualTo(0)
    }

    @Test
    fun `formatRemaining produces M_SS form`() {
        assertThat(SleepTimerMath.formatRemaining(0)).isEqualTo("0:00")
        assertThat(SleepTimerMath.formatRemaining(45_000)).isEqualTo("0:45")
        assertThat(SleepTimerMath.formatRemaining(60_000)).isEqualTo("1:00")
        assertThat(SleepTimerMath.formatRemaining(15L * 60L * 1000L)).isEqualTo("15:00")
        assertThat(SleepTimerMath.formatRemaining((15L * 60L + 1L) * 1000L - 1)).isEqualTo("15:00")
    }

    @Test
    fun `formatRemaining clamps negative to zero`() {
        // Tick lands a frame after the VM's pause action — remaining
        // can be briefly -50 ms. UI should never render `-0:01`.
        assertThat(SleepTimerMath.formatRemaining(-50)).isEqualTo("0:00")
        assertThat(SleepTimerMath.formatRemaining(-999_999)).isEqualTo("0:00")
    }

    @Test
    fun `isExpired fires at zero and below`() {
        // Equal-to-zero counts as expired so the pause fires on the
        // exact-tick case, not one tick later.
        assertThat(SleepTimerMath.isExpired(0)).isTrue()
        assertThat(SleepTimerMath.isExpired(-1)).isTrue()
        assertThat(SleepTimerMath.isExpired(1)).isFalse()
        assertThat(SleepTimerMath.isExpired(60_000)).isFalse()
    }

    @Test
    fun `quick picks cover the typical sleep span`() {
        // Sanity check: the picker should always include 15 (a quick
        // nap), 30 (an episode), 60 (an album side), and at least one
        // longer option for movies. Locks down the public list shape
        // — UI iterates this and renders one chip per entry.
        assertThat(SleepTimerMath.QUICK_PICKS_MIN).containsExactly(15, 30, 45, 60, 90).inOrder()
    }
}
