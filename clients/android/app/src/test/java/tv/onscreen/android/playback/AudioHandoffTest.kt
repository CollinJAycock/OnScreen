package tv.onscreen.android.playback

import android.content.Context
import com.google.common.truth.Truth.assertThat
import io.mockk.mockk
import io.mockk.every
import io.mockk.verify
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.common.Player
import org.junit.After
import org.junit.Before
import org.junit.Test

/** The handoff slot releases a player in two places, and must end the
 *  server session that player was reading in both. */
class AudioHandoffTest {

    private val ended = mutableListOf<StreamSession>()

    private fun session(id: String) = StreamSession(id = id, token = "tok-$id", offsetMs = 0L, playlistUrl = "http://srv/$id.m3u8")

    private fun meta(itemId: String, session: StreamSession? = null) = AudioHandoff.Metadata(
        itemId = itemId,
        itemType = "track",
        parentId = "album-1",
        index = 1,
        hlsOffsetMs = 0L,
        session = session,
    )

    @Before
    fun setUp() {
        AudioHandoff.clear()
        AudioHandoff.sessionEnder = { ended += it }
    }

    @After
    fun tearDown() {
        AudioHandoff.clear()
        AudioHandoff.sessionEnder = null
        AudioHandoff.releaseReporter = null
    }

    @Test
    fun `a player evicted by a new park is released and its session ended`() {
        val first = mockk<ExoPlayer>(relaxed = true)
        val second = mockk<ExoPlayer>(relaxed = true)
        AudioHandoff.park(first, meta("t1", session("s1")))

        AudioHandoff.park(second, meta("t2"))

        verify(exactly = 1) { first.release() }
        assertThat(ended).containsExactly(session("s1"))
        assertThat(AudioHandoff.peekMetadata()?.itemId).isEqualTo("t2")
    }

    @Test
    fun `re-parking the same player keeps its session`() {
        val player = mockk<ExoPlayer>(relaxed = true)
        AudioHandoff.park(player, meta("t1", session("s1")))

        AudioHandoff.park(player, meta("t1", session("s1")))

        verify(exactly = 0) { player.release() }
        assertThat(ended).isEmpty()
    }

    @Test
    fun `a taken player takes its session along`() {
        val player = mockk<ExoPlayer>(relaxed = true)
        AudioHandoff.park(player, meta("t1", session("s1")))
        val meta = AudioHandoff.peekMetadata()

        assertThat(AudioHandoff.take("t1")).isSameInstanceAs(player)

        assertThat(meta?.session).isEqualTo(session("s1"))
        assertThat(ended).isEmpty()
    }

    @Test
    fun `stopping everything on sign-out ends the parked session`() {
        val player = mockk<ExoPlayer>(relaxed = true)
        AudioHandoff.park(player, meta("t1", session("s1")))

        AudioHandoff.stopAll(mockk<Context>(relaxed = true))

        verify(exactly = 1) { player.release() }
        assertThat(ended).containsExactly(session("s1"))
        assertThat(AudioHandoff.peek()).isNull()
    }

    @Test
    fun `a direct-play player has nothing to end`() {
        AudioHandoff.park(mockk(relaxed = true), meta("t1"))
        AudioHandoff.park(mockk(relaxed = true), meta("t2"))

        assertThat(ended).isEmpty()
    }

    @Test
    fun `each park has its own generation, ended by a take or a new park`() {
        val player = mockk<ExoPlayer>(relaxed = true)
        val first = AudioHandoff.park(player, meta("t1"))
        assertThat(AudioHandoff.isParked(first)).isTrue()

        AudioHandoff.take("t1")
        assertThat(AudioHandoff.isParked(first)).isFalse()

        // The same player handed over again is a new park.
        val second = AudioHandoff.park(player, meta("t1"))
        assertThat(second).isGreaterThan(first)
        assertThat(AudioHandoff.generation()).isEqualTo(second)
        assertThat(AudioHandoff.isParked(first)).isFalse()
        assertThat(AudioHandoff.isParked(second)).isTrue()
    }

    @Test
    fun `a player released from the slot reports where it got to`() {
        val reported = mutableListOf<Triple<String, Long, Long>>()
        AudioHandoff.releaseReporter = { m, pos, dur -> reported += Triple(m.itemId, pos, dur) }
        val first = mockk<ExoPlayer>(relaxed = true) {
            every { currentPosition } returns 40_000L
            every { duration } returns 300_000L
            every { playbackState } returns Player.STATE_READY
        }
        AudioHandoff.park(first, meta("t1", session("s1")).copy(hlsOffsetMs = 60_000L, itemDurationMs = 360_000L))

        AudioHandoff.park(mockk(relaxed = true), meta("t2"))

        // Content time: 40 s into a session opened at 60 s.
        assertThat(reported).containsExactly(Triple("t1", 100_000L, 360_000L))
    }

    @Test
    fun `a finished player is not reported again`() {
        val reported = mutableListOf<String>()
        AudioHandoff.releaseReporter = { m, _, _ -> reported += m.itemId }
        val done = mockk<ExoPlayer>(relaxed = true) {
            every { currentPosition } returns 317_000L
            every { duration } returns 317_000L
            every { playbackState } returns Player.STATE_ENDED
        }
        AudioHandoff.park(done, meta("t1"))

        AudioHandoff.stopAll(mockk<Context>(relaxed = true))

        assertThat(reported).isEmpty()
    }

    @Test
    fun `a player belongs to its attacher only while its park holds the slot`() {
        val player = mockk<ExoPlayer>(relaxed = true)
        val first = AudioHandoff.park(player, meta("t1"))
        assertThat(AudioHandoff.owns(player, first)).isTrue()
        assertThat(AudioHandoff.owns(mockk(relaxed = true), first)).isFalse()

        // Taken back and parked again: the old attach no longer owns it.
        AudioHandoff.take("t1")
        assertThat(AudioHandoff.owns(player, first)).isFalse()
        val second = AudioHandoff.park(player, meta("t1"))
        assertThat(AudioHandoff.owns(player, first)).isFalse()
        assertThat(AudioHandoff.owns(player, second)).isTrue()
    }

    @Test
    fun `releaseIfParked lets go only of its own park`() {
        val player = mockk<ExoPlayer>(relaxed = true)
        val stale = AudioHandoff.park(player, meta("t1", session("s1")))
        AudioHandoff.take("t1")
        val current = AudioHandoff.park(player, meta("t1", session("s1")))

        AudioHandoff.releaseIfParked(stale)
        verify(exactly = 0) { player.release() }
        assertThat(ended).isEmpty()

        AudioHandoff.releaseIfParked(current)
        verify(exactly = 1) { player.release() }
        assertThat(ended).containsExactly(session("s1"))
        assertThat(AudioHandoff.peek()).isNull()
    }
}
