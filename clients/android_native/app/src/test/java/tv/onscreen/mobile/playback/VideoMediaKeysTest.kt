package tv.onscreen.mobile.playback

import androidx.media3.common.Player
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import org.junit.Test

/**
 * Media keys during a video: the service's backstop ([ServiceMediaKeys]) and
 * the video session's view of its player ([KeysOnlySessionPlayer]).
 */
class VideoMediaKeysTest {

    // ── The video session's player ─────────────────────────────────────

    @Test
    fun `a key starts the video only while its screen is up`() {
        val video = mockk<Player>(relaxed = true)
        var onScreen = false
        val keys = KeysOnlySessionPlayer(video) { onScreen }

        keys.play()
        keys.playWhenReady = true
        io.mockk.verify(exactly = 0) { video.play() }
        io.mockk.verify(exactly = 0) { video.playWhenReady = true }

        // Pausing always goes through.
        keys.pause()
        keys.playWhenReady = false
        io.mockk.verify { video.pause() }
        io.mockk.verify { video.playWhenReady = false }

        onScreen = true
        keys.play()
        keys.playWhenReady = true
        io.mockk.verify { video.play() }
        io.mockk.verify { video.playWhenReady = true }
    }

    // ── The service's backstop ────────────────────────────────────────

    @Test
    fun `with no video up the service handles every key`() {
        for (state in listOf(Player.STATE_IDLE, Player.STATE_BUFFERING, Player.STATE_READY, Player.STATE_ENDED)) {
            assertThat(ServiceMediaKeys.ignore(false, playWhenReady = true, playbackState = state)).isFalse()
            assertThat(ServiceMediaKeys.ignore(false, playWhenReady = false, playbackState = state)).isFalse()
        }
    }

    @Test
    fun `a key over a video does not restart an ended book`() {
        // The reported bug: the sleep timer ended the chapter (ENDED, with
        // playWhenReady still true), a video started, and a headset play
        // restarted the chapter from 0:00 underneath it.
        assertThat(ServiceMediaKeys.ignore(true, playWhenReady = true, playbackState = Player.STATE_ENDED)).isTrue()
        assertThat(ServiceMediaKeys.ignore(true, playWhenReady = false, playbackState = Player.STATE_ENDED)).isTrue()
    }

    @Test
    fun `a key over a video does not resume a paused or stopped book`() {
        assertThat(ServiceMediaKeys.ignore(true, playWhenReady = false, playbackState = Player.STATE_READY)).isTrue()
        assertThat(ServiceMediaKeys.ignore(true, playWhenReady = false, playbackState = Player.STATE_IDLE)).isTrue()
        assertThat(ServiceMediaKeys.ignore(true, playWhenReady = true, playbackState = Player.STATE_IDLE)).isTrue()
    }

    @Test
    fun `audio that is audibly playing over a video still pauses`() {
        assertThat(ServiceMediaKeys.ignore(true, playWhenReady = true, playbackState = Player.STATE_READY)).isFalse()
        assertThat(ServiceMediaKeys.ignore(true, playWhenReady = true, playbackState = Player.STATE_BUFFERING)).isFalse()
    }

    // ── The video session's player ────────────────────────────────────

    @Test
    fun `the video session sees no item, timeline or metadata`() {
        val player = mockk<Player>().also { every { it.isCommandAvailable(any()) } returns true }
        val keysOnly = KeysOnlySessionPlayer(player)

        // What media3's legacy bridge reads the item url and the queue with.
        assertThat(keysOnly.isCommandAvailable(Player.COMMAND_GET_CURRENT_MEDIA_ITEM)).isFalse()
        assertThat(keysOnly.isCommandAvailable(Player.COMMAND_GET_TIMELINE)).isFalse()
        assertThat(keysOnly.isCommandAvailable(Player.COMMAND_GET_METADATA)).isFalse()
    }

    @Test
    fun `the video session still plays, pauses and seeks`() {
        val player = mockk<Player>().also {
            every { it.isCommandAvailable(any()) } returns true
            every { it.isCommandAvailable(Player.COMMAND_STOP) } returns false
        }
        val keysOnly = KeysOnlySessionPlayer(player)

        assertThat(keysOnly.isCommandAvailable(Player.COMMAND_PLAY_PAUSE)).isTrue()
        assertThat(keysOnly.isCommandAvailable(Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM)).isTrue()
        // Otherwise it answers as the real player does.
        assertThat(keysOnly.isCommandAvailable(Player.COMMAND_STOP)).isFalse()
    }
}
