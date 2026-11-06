package tv.onscreen.mobile.ui.player

import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.MediaSource
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import org.junit.Test

/**
 * The PlayerView's controls on a session read the content's clock
 * ([ContentTimePlayer]): a film resumed at 1:00:00 shows 1:00:00 of 2:00:00,
 * not 0:00 of whatever the server has written so far.
 */
class ContentTimePlayerTest {

    private val inner = mockk<Player>(relaxed = true).also {
        every { it.currentMediaItemIndex } returns 0
        every { it.currentPosition } returns 5_000L
        every { it.contentPosition } returns 5_000L
        every { it.bufferedPosition } returns 30_000L
        every { it.duration } returns 120_000L // what the session has written
    }
    private val outside = mutableListOf<Long>()
    private val player = ContentTimePlayer(
        inner = inner,
        offsetMs = { 3_600_000L },
        contentLengthMs = { 7_200_000L },
        seekOutside = { outside += it; true },
    )

    @Test
    fun `positions count from the content's start`() {
        assertThat(player.currentPosition).isEqualTo(3_605_000L)
        assertThat(player.contentPosition).isEqualTo(3_605_000L)
        assertThat(player.bufferedPosition).isEqualTo(3_630_000L)
    }

    @Test
    fun `the length is the content's, not the session's growing playlist`() {
        assertThat(player.duration).isEqualTo(7_200_000L)
        assertThat(player.contentDuration).isEqualTo(7_200_000L)
    }

    @Test
    fun `an unknown content length falls back to the player's`() {
        val p = ContentTimePlayer(inner, { 0L }, { 0L }, { false })
        assertThat(p.duration).isEqualTo(120_000L)
    }

    @Test
    fun `a seek inside the session goes to it in the session's time`() {
        player.seekTo(3_700_000L)
        verify { inner.seekTo(0, 100_000L) }
        assertThat(outside).isEmpty()
    }

    @Test
    fun `a seek before the session's start starts a session there`() {
        player.seekTo(600_000L)
        assertThat(outside).containsExactly(600_000L)
        verify(exactly = 0) { inner.seekTo(any(), any()) }
    }
}

/** A session's player hands a seek past what's been written to a new session. */
class HlsSessionPlayerSeekBeyondTest {

    private val exo = mockk<ExoPlayer>(relaxed = true).also {
        every { it.currentMediaItemIndex } returns 0
        every { it.duration } returns 120_000L // written so far
    }
    private val beyond = mutableListOf<Long>()
    private val player = HlsSessionPlayer(exo, seekBeyond = { beyond += it; true }) { mockk<MediaSource>() }

    @Test
    fun `a seek within the written range plays there`() {
        player.seekTo(60_000L)
        verify { exo.seekTo(60_000L) }
        assertThat(beyond).isEmpty()
    }

    @Test
    fun `a seek past the written range restarts the session there`() {
        player.seekTo(1_800_000L)
        assertThat(beyond).containsExactly(1_800_000L)
        verify(exactly = 0) { exo.seekTo(any<Long>()) }
        verify(exactly = 0) { exo.seekTo(any(), any()) }
    }
}
