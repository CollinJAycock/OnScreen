package tv.onscreen.android.ui.playback

import androidx.leanback.media.PlayerAdapter
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.common.util.Util
import androidx.media3.ui.leanback.LeanbackPlayerAdapter
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import org.junit.Test

/**
 * The transport bar's Play once the item has ended: LeanbackPlayerAdapter
 * hands it to Util.handlePlayButtonAction, which seeks to the default
 * position. Left to the player, a server session's default is its own head
 * (the resume point of a resumed one), or, while it is still being written,
 * a point that trails its live edge (TranscodeHls.mediaItem).
 *
 * Its scrub: the adapter seeks with the current item's index, which a
 * ForwardingPlayer passes straight on. And its Play after an error, which
 * prepares the idle player first; the remote's Play goes the same way then.
 */
@androidx.annotation.OptIn(UnstableApi::class)
class ContentTimeForwardingPlayerTest {

    private val inner = mockk<Player>(relaxed = true).apply {
        every { playbackState } returns Player.STATE_ENDED
        every { isCommandAvailable(any()) } returns true
        every { duration } returns 60_000L
        every { currentMediaItemIndex } returns 0
    }

    private var offset = 0L
    private val reissuedAt = mutableListOf<Long>()
    private var reloads = 0
    private var reloaded = true

    private val player = ContentTimeForwardingPlayer(
        inner,
        offsetMs = { offset },
        contentDurationMs = { 7_200_000L },
        onSeekOutsideWindow = { reissuedAt += it },
        reloadStopped = { reloads++; reloaded },
    )

    /** The transport bar, as the fragment builds it (the glue is its callback). */
    private val adapter = LeanbackPlayerAdapter(mockk(relaxed = true), player, 16).apply {
        callback = object : PlayerAdapter.Callback() {}
    }

    @Test
    fun `Play at the end replays the item from its start`() {
        Util.handlePlayButtonAction(player)

        verify { inner.seekTo(0L) }
        verify { inner.play() }
        verify(exactly = 0) { inner.seekToDefaultPosition() }
        assertThat(reissuedAt).isEmpty()
    }

    @Test
    fun `Play at the end of a resumed session re-issues it from 0-00`() {
        // Opened at 45:00: the session's own head is the resume point.
        offset = 2_700_000L
        Util.handlePlayButtonAction(player)

        assertThat(reissuedAt).containsExactly(0L)
        verify(exactly = 0) { inner.seekTo(any<Long>()) }
        verify(exactly = 0) { inner.seekToDefaultPosition() }
    }

    @Test
    fun `the default position of the item playing is its start too`() {
        player.seekToDefaultPosition(0)
        verify { inner.seekTo(0L) }
        verify(exactly = 0) { inner.seekToDefaultPosition(any()) }
    }

    @Test
    fun `a scrub on a resumed session lands in session time`() {
        // Resumed at 45:00, ten minutes transcoded so far: 50:00 is 5:00 in.
        offset = 2_700_000L
        every { inner.duration } returns 600_000L

        adapter.seekTo(3_000_000L)

        verify { inner.seekTo(300_000L) }
        verify(exactly = 0) { inner.seekTo(any(), any()) }
        assertThat(reissuedAt).isEmpty()
    }

    @Test
    fun `a scrub outside a resumed session's window re-issues it there`() {
        offset = 2_700_000L
        // Still being written.
        every { inner.isCurrentMediaItemDynamic } returns true

        // Past the minute transcoded so far, then before the resume point.
        adapter.seekTo(3_000_000L)
        adapter.seekTo(600_000L)

        assertThat(reissuedAt).containsExactly(3_000_000L, 600_000L).inOrder()
        verify(exactly = 0) { inner.seekTo(any<Long>()) }
        verify(exactly = 0) { inner.seekTo(any(), any()) }
    }

    @Test
    fun `a scrub past the end of a finished session ends it instead of re-issuing it`() {
        // Resumed at 45:00 and written to its end: 1:14:10 of segments, a
        // little short of the item's listed 2:00:00, where the scrub's last
        // step lands. Re-issued there, a transcode started at the end of the file.
        offset = 2_700_000L
        every { inner.duration } returns 4_450_000L
        every { inner.isCurrentMediaItemDynamic } returns false

        adapter.seekTo(7_200_000L)

        verify { inner.seekTo(4_450_000L) }
        assertThat(reissuedAt).isEmpty()
    }

    @Test
    fun `before a finished session's window still re-issues it`() {
        offset = 2_700_000L
        every { inner.duration } returns 4_450_000L
        every { inner.isCurrentMediaItemDynamic } returns false

        adapter.seekTo(600_000L)

        assertThat(reissuedAt).containsExactly(600_000L)
        verify(exactly = 0) { inner.seekTo(any<Long>()) }
    }

    @Test
    fun `a scrub past the end of a direct play clamps to its end`() {
        adapter.seekTo(61_000L)

        verify { inner.seekTo(60_000L) }
        assertThat(reissuedAt).isEmpty()
    }

    @Test
    fun `a seek to another item passes through`() {
        offset = 2_700_000L

        player.seekTo(1, 5_000L)

        verify { inner.seekTo(1, 5_000L) }
        assertThat(reissuedAt).isEmpty()
    }

    @Test
    fun `Play after an error loads the session again instead of preparing it`() {
        every { inner.playbackState } returns Player.STATE_IDLE

        // The transport bar's Play on the stopped player.
        adapter.play()

        assertThat(reloads).isEqualTo(1)
        verify(exactly = 0) { inner.prepare() }
        verify { inner.play() }
    }

    @Test
    fun `Play after an error on direct play prepares it again`() {
        every { inner.playbackState } returns Player.STATE_IDLE
        reloaded = false

        adapter.play()

        assertThat(reloads).isEqualTo(1)
        verify { inner.prepare() }
        verify { inner.play() }
    }

    @Test
    fun `a prepared player keeps its source`() {
        every { inner.playbackState } returns Player.STATE_READY

        player.prepare()

        assertThat(reloads).isEqualTo(0)
        verify { inner.prepare() }
    }

    // --- the remote's Play (PlaybackHelper.playFromKey on the raw player) ---

    @Test
    fun `the remote's Play after an error loads the session again, as the transport's does`() {
        every { inner.playbackState } returns Player.STATE_IDLE
        every { inner.playerError } returns mockk<PlaybackException>()

        PlaybackHelper.playFromKey(inner, adapter)

        assertThat(reloads).isEqualTo(1)
        verify(exactly = 0) { inner.prepare() }
        verify { inner.play() }
    }

    @Test
    fun `the remote's Play after an error on direct play prepares it again`() {
        every { inner.playbackState } returns Player.STATE_IDLE
        every { inner.playerError } returns mockk<PlaybackException>()
        reloaded = false

        PlaybackHelper.playFromKey(inner, adapter)

        verify { inner.prepare() }
        verify { inner.play() }
    }

    @Test
    fun `the remote's Play before the first source only plays`() {
        // Prepared with nothing in it, the player would end at once.
        every { inner.playbackState } returns Player.STATE_IDLE
        every { inner.playerError } returns null

        PlaybackHelper.playFromKey(inner, adapter)

        assertThat(reloads).isEqualTo(0)
        verify(exactly = 0) { inner.prepare() }
        verify { inner.play() }
    }

    @Test
    fun `the remote's Play with no transport (playback refused) only plays`() {
        every { inner.playbackState } returns Player.STATE_IDLE
        every { inner.playerError } returns mockk<PlaybackException>()

        PlaybackHelper.playFromKey(inner, null)

        assertThat(reloads).isEqualTo(0)
        verify(exactly = 0) { inner.prepare() }
        verify { inner.play() }
    }

    @Test
    fun `the remote's Play on a paused player plays it`() {
        every { inner.playbackState } returns Player.STATE_READY

        PlaybackHelper.playFromKey(inner, adapter)

        assertThat(reloads).isEqualTo(0)
        verify(exactly = 0) { inner.prepare() }
        verify(exactly = 0) { inner.seekTo(any<Long>()) }
        verify { inner.play() }
    }
}
