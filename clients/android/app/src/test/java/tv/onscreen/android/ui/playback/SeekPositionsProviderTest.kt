package tv.onscreen.android.ui.playback

import android.content.Context
import androidx.leanback.media.PlaybackTransportControlGlue
import androidx.leanback.widget.PlaybackSeekUi
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.leanback.LeanbackPlayerAdapter
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import io.mockk.verifyOrder
import org.junit.Test

/**
 * The transport bar's scrub on a title without trickplay thumbnails. With no
 * seek provider, leanback's glue seeks the player at every scrub step: on a
 * resumed server session, the first step past the transcoded window
 * re-issued the session there. With one (positions only) it seeks once, where
 * the scrub was confirmed, and never for a scrub cancelled with Back.
 *
 * The scrubs here drive the glue as the transport row does (its
 * PlaybackSeekUi.Client), through the adapter and player the fragment builds.
 */
@androidx.annotation.OptIn(UnstableApi::class)
class SeekPositionsProviderTest {

    @Test
    fun `positions step through the runtime in hundredths from 0`() {
        val positions = SeekPositionsProvider.evenlySpaced(7_200_000L)

        assertThat(positions).hasLength(101)
        assertThat(positions.first()).isEqualTo(0L)
        assertThat(positions[1]).isEqualTo(72_000L)
        assertThat(positions[99]).isEqualTo(7_128_000L)
        // The end itself, so a scrub can reach it (Up Next, a finished
        // session's ENDED).
        assertThat(positions.last()).isEqualTo(7_200_000L)
        assertThat(positions.asList()).isInStrictOrder()
    }

    @Test
    fun `no positions while the duration is unknown`() {
        assertThat(SeekPositionsProvider.evenlySpaced(0L)).isEmpty()
        // LeanbackPlayerAdapter's "unknown".
        assertThat(SeekPositionsProvider.evenlySpaced(-1L)).isEmpty()
    }

    @Test
    fun `a duration shorter than the steps is its start and its end`() {
        assertThat(SeekPositionsProvider.evenlySpaced(40L).asList()).containsExactly(0L, 40L).inOrder()
    }

    @Test
    fun `positions follow the duration as each scrub starts`() {
        // A server session with no listed length: its window grows.
        var duration = 600_000L
        val provider = SeekPositionsProvider { duration }
        assertThat(provider.seekPositions[1]).isEqualTo(6_000L)

        duration = 1_200_000L
        assertThat(provider.seekPositions[1]).isEqualTo(12_000L)
    }

    // --- the glue ---

    private val inner = mockk<Player>(relaxed = true).apply {
        every { playbackState } returns Player.STATE_READY
        every { playWhenReady } returns true
        every { isCommandAvailable(any()) } returns true
        every { currentMediaItemIndex } returns 0
        // Resumed at 45:00, a minute transcoded so far, still being written.
        every { duration } returns 60_000L
        every { isCurrentMediaItemDynamic } returns true
    }

    private var offset = 2_700_000L
    private val reissuedAt = mutableListOf<Long>()

    private val player = ContentTimeForwardingPlayer(
        inner,
        offsetMs = { offset },
        contentDurationMs = { 7_200_000L },
        onSeekOutsideWindow = { reissuedAt += it },
        reloadStopped = { false },
    )

    private val adapter = LeanbackPlayerAdapter(mockk(relaxed = true), player, 16)

    private val glue = PlaybackTransportControlGlue(mockk<Context>(relaxed = true), adapter).apply {
        isSeekEnabled = true
        seekProvider = SeekPositionsProvider { adapter.duration }
    }

    /** The transport row's side of the scrub. The glue hands it to its host
     *  (PlaybackSeekUi) as it attaches, which builds a controls row this JVM
     *  can't (its adapters' observers are android.database.Observable's). */
    private val seekUi = PlaybackTransportControlGlue::class.java
        .getDeclaredField("mPlaybackSeekUiClient")
        .apply { isAccessible = true }
        .get(glue) as PlaybackSeekUi.Client

    /** A scrub through [steps] (content time), confirmed, or cancelled with Back. */
    private fun scrub(vararg steps: Long, cancel: Boolean = false) {
        val client = seekUi
        assertThat(client.isSeekEnabled).isTrue()
        client.onSeekStarted()
        steps.forEach { client.onSeekPositionChanged(it) }
        client.onSeekFinished(cancel)
    }

    @Test
    fun `a scrub past a resumed session's window re-issues it once, where it stopped`() {
        // 46:12, 47:24, 48:36 ... 1:00:00: all past the minute written so far.
        scrub(2_772_000L, 2_844_000L, 2_916_000L, 3_600_000L)

        assertThat(reissuedAt).containsExactly(3_600_000L)
        verify(exactly = 0) { inner.seekTo(any<Long>()) }
        verify(exactly = 0) { inner.seekTo(any(), any()) }
    }

    @Test
    fun `a scrub cancelled with Back seeks nowhere`() {
        scrub(2_772_000L, 3_600_000L, cancel = true)

        assertThat(reissuedAt).isEmpty()
        verify(exactly = 0) { inner.seekTo(any<Long>()) }
        verify(exactly = 0) { inner.seekTo(any(), any()) }
    }

    @Test
    fun `with no provider every step seeks on its own`() {
        // Why the fragment never leaves the glue without one.
        glue.seekProvider = null

        scrub(2_772_000L, 2_844_000L, 3_600_000L)

        // The first step past the edge re-issued the session there: the
        // view model drops the rest while that is in flight.
        assertThat(reissuedAt).containsExactly(2_772_000L, 2_844_000L, 3_600_000L).inOrder()
    }

    @Test
    fun `a direct-play scrub seeks once, where it stopped`() {
        offset = 0L
        every { inner.duration } returns 7_200_000L
        every { inner.isCurrentMediaItemDynamic } returns false

        scrub(72_000L, 144_000L, 3_600_000L)

        verify(exactly = 1) { inner.seekTo(any<Long>()) }
        verifyOrder {
            inner.pause()
            inner.seekTo(3_600_000L)
            inner.play()
        }
        assertThat(reissuedAt).isEmpty()
    }
}
