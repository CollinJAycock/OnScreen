package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.Chapter

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
    fun `EndOfChapter runs no countdown and waits for the item like EndOfTrack`() {
        assertThat(SleepTimerMath.initialMs(SleepTimer.EndOfChapter)).isEqualTo(0)
        assertThat(SleepTimerMath.endsWithItem(SleepTimer.EndOfChapter)).isTrue()
        assertThat(SleepTimerMath.endsWithItem(SleepTimer.EndOfTrack)).isTrue()
        assertThat(SleepTimerMath.endsWithItem(SleepTimer.Minutes(15))).isFalse()
        assertThat(SleepTimerMath.endsWithItem(null)).isFalse()
    }

    private val chapters = listOf(
        Chapter("One", 0, 60_000),
        Chapter("Two", 60_000, 150_000),
        Chapter("Three", 150_000, 200_000),
    )

    @Test
    fun `end of chapter is the next chapter's start`() {
        assertThat(SleepTimerMath.chapterEndMs(chapters, 0)).isEqualTo(60_000)
        assertThat(SleepTimerMath.chapterEndMs(chapters, 59_999)).isEqualTo(60_000)
        assertThat(SleepTimerMath.chapterEndMs(chapters, 100_000)).isEqualTo(150_000)
    }

    @Test
    fun `set right on a chapter mark it waits for that chapter's end`() {
        // A chapter just started (or was just jumped to) — not the mark
        // already crossed.
        assertThat(SleepTimerMath.chapterEndMs(chapters, 60_000)).isEqualTo(150_000)
        assertThat(SleepTimerMath.chapterEndMs(chapters, 60_001)).isEqualTo(150_000)
    }

    @Test
    fun `in the last chapter it stops at that chapter's end or the item's`() {
        assertThat(SleepTimerMath.chapterEndMs(chapters, 170_000)).isEqualTo(200_000)
        // At / past the last mark: the item's own end stops it (STATE_ENDED).
        assertThat(SleepTimerMath.chapterEndMs(chapters, 200_000)).isNull()
        assertThat(SleepTimerMath.chapterEndMs(chapters, 250_000)).isNull()
    }

    @Test
    fun `no chapter marks means the end of the item`() {
        // Every multi-file chapter, and a single file without marks.
        assertThat(SleepTimerMath.chapterEndMs(emptyList(), 5_000)).isNull()
    }

    @Test
    fun `chapter marks out of order still give the next boundary`() {
        val shuffled = listOf(chapters[2], chapters[0], chapters[1])
        assertThat(SleepTimerMath.chapterEndMs(shuffled, 10_000)).isEqualTo(60_000)
    }

    @Test
    fun `the watch wakes in time for the mark at any speed`() {
        // 10 s of book at 2× is 5 s of wall time: capped to a second.
        assertThat(SleepTimerMath.nextCheckDelayMs(110_000, 100_000, 2f)).isEqualTo(1_000)
        // 600 ms of book at 3× is 200 ms of wall time.
        assertThat(SleepTimerMath.nextCheckDelayMs(100_600, 100_000, 3f)).isEqualTo(200)
        // Past the mark, or a player reporting speed 0: never a tight loop.
        assertThat(SleepTimerMath.nextCheckDelayMs(100_000, 100_500, 1f)).isEqualTo(50)
        assertThat(SleepTimerMath.nextCheckDelayMs(100_600, 100_000, 0f)).isEqualTo(600)
        // Nothing to aim at: poll for a seek once a second.
        assertThat(SleepTimerMath.nextCheckDelayMs(null, 100_000, 1f)).isEqualTo(1_000)
    }

    @Test
    fun `a countdown handed to the next screen loses the time in between`() {
        SleepTimerCarry.put("ch-2", SleepTimer.Minutes(30), 600_000, nowMs = 1_000)
        val taken = SleepTimerCarry.take("ch-2", nowMs = 3_500)
        assertThat(taken).isEqualTo(SleepTimerState(SleepTimer.Minutes(30), 597_500))
        // One-shot.
        assertThat(SleepTimerCarry.take("ch-2", nowMs = 3_500)).isNull()
    }

    @Test
    fun `a handed-over timer is only for the item it was handed to, and not for long`() {
        SleepTimerCarry.put("ch-2", SleepTimer.EndOfChapter, 0, nowMs = 0)
        assertThat(SleepTimerCarry.take("ch-9", nowMs = 100)).isNull()
        assertThat(SleepTimerCarry.take("ch-2", nowMs = 100))
            .isEqualTo(SleepTimerState(SleepTimer.EndOfChapter, 0))

        SleepTimerCarry.put("ch-3", SleepTimer.EndOfTrack, 0, nowMs = 0)
        assertThat(SleepTimerCarry.take("ch-3", nowMs = 60_000)).isNull()
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
