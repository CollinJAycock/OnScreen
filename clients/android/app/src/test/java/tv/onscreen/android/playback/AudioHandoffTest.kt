package tv.onscreen.android.playback

import android.content.Context
import com.google.common.truth.Truth.assertThat
import io.mockk.mockk
import io.mockk.verify
import androidx.media3.exoplayer.ExoPlayer
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
}
