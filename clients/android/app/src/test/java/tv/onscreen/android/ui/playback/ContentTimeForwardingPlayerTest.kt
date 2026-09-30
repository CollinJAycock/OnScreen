package tv.onscreen.android.ui.playback

import android.content.Context
import androidx.media3.common.C
import androidx.media3.common.Player
import androidx.media3.ui.leanback.LeanbackPlayerAdapter
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import org.junit.Test

class ContentTimeForwardingPlayerTest {

    // A 2-hour film resumed at 45:00, with 10 minutes transcoded so far.
    private var offset = 2_700_000L

    private val inner = mockk<Player>(relaxed = true).apply {
        every { currentMediaItemIndex } returns 0
        every { duration } returns 600_000L
    }

    private val reissued = mutableListOf<Long>()

    private val player = ContentTimeForwardingPlayer(
        inner,
        offsetMs = { offset },
        contentDurationMs = { 7_200_000L },
        onSeekOutsideWindow = { reissued += it },
    )

    // The real adapter, so the scrub path is exercised the way the glue
    // drives it — it seeks through seekTo(mediaItemIndex, positionMs).
    private val adapter = LeanbackPlayerAdapter(mockk<Context>(relaxed = true), player, 16)

    @Test
    fun `a scrub on a resumed session lands in session time`() {
        adapter.seekTo(3_000_000L)
        verify { inner.seekTo(0, 300_000L) }
        assertThat(reissued).isEmpty()
    }

    @Test
    fun `scrubbing back before the resume point re-issues the session there`() {
        adapter.seekTo(600_000L)
        assertThat(reissued).containsExactly(600_000L)
        verify(exactly = 0) { inner.seekTo(any<Int>(), any<Long>()) }
    }

    @Test
    fun `scrubbing past the transcoded edge re-issues instead of undershooting`() {
        adapter.seekTo(3_600_000L)
        assertThat(reissued).containsExactly(3_600_000L)
        verify(exactly = 0) { inner.seekTo(any<Int>(), any<Long>()) }
    }

    @Test
    fun `the resume point and the edge are both inside the window`() {
        player.seekTo(0, 2_700_000L)
        verify { inner.seekTo(0, 0L) }
        player.seekTo(0, 3_300_000L)
        verify { inner.seekTo(0, 600_000L) }
        assertThat(reissued).isEmpty()
    }

    @Test
    fun `an unknown window length doesn't bound the seek`() {
        every { inner.duration } returns C.TIME_UNSET
        player.seekTo(0, 5_000_000L)
        verify { inner.seekTo(0, 2_300_000L) }
        assertThat(reissued).isEmpty()
    }

    @Test
    fun `a seek to the default position isn't read as a time`() {
        player.seekTo(0, C.TIME_UNSET)
        verify { inner.seekTo(0, C.TIME_UNSET) }
        assertThat(reissued).isEmpty()
    }

    @Test
    fun `direct play passes straight through`() {
        offset = 0L
        every { inner.duration } returns 7_200_000L
        adapter.seekTo(12_000L)
        verify { inner.seekTo(0, 12_000L) }
        assertThat(reissued).isEmpty()
    }

    @Test
    fun `the plain overload translates the same way`() {
        player.seekTo(3_000_000L)
        verify { inner.seekTo(300_000L) }
        player.seekTo(60_000L)
        assertThat(reissued).containsExactly(60_000L)
    }
}
