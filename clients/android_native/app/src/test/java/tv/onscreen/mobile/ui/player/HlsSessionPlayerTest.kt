package tv.onscreen.mobile.ui.player

import androidx.media3.common.Player
import androidx.media3.common.util.Util
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.MediaSource
import com.google.common.truth.Truth.assertThat
import io.mockk.Runs
import io.mockk.every
import io.mockk.just
import io.mockk.mockk
import io.mockk.slot
import io.mockk.verify
import io.mockk.verifyOrder
import org.junit.Test
import tv.onscreen.mobile.playback.KeysOnlySessionPlayer

/**
 * A remux / transcode session's player keeps Media3 off the live window's
 * default position ([HlsSessionPlayer]; HlsStartTest shows where that
 * default drifts to).
 */
class HlsSessionPlayerTest {

    private val exo = mockk<ExoPlayer>(relaxed = true).also {
        every { it.isCommandAvailable(any()) } returns true
        every { it.currentMediaItemIndex } returns 0
    }
    private val newSource = mockk<MediaSource>()
    private val player = HlsSessionPlayer(exo) { newSource }

    @Test
    fun `Play on an ended stream plays it again from its start`() {
        every { exo.playbackState } returns Player.STATE_ENDED

        // What the controller's play button runs.
        Util.handlePlayButtonAction(player)

        verify { exo.seekTo(0, 0L) }
        verify(exactly = 0) { exo.seekToDefaultPosition() }
        verify(exactly = 0) { exo.seekToDefaultPosition(any()) }
        verify { exo.play() }
    }

    @Test
    fun `a default-position seek goes to the stream's start`() {
        player.seekToDefaultPosition(0)

        verify { exo.seekTo(0, 0L) }
        verify(exactly = 0) { exo.seekToDefaultPosition(any()) }
    }

    @Test
    fun `Next is withdrawn and does nothing`() {
        assertThat(player.isCommandAvailable(Player.COMMAND_SEEK_TO_NEXT)).isFalse()

        player.seekToNext()

        verify(exactly = 0) { exo.seekToNext() }
        verify(exactly = 0) { exo.seekToDefaultPosition(any()) }
        verify(exactly = 0) { exo.seekTo(any(), any()) }
    }

    @Test
    fun `listeners hear the commands without Next`() {
        // The key session takes the commands the player's event carries, not
        // availableCommands: the event's own still offered Next.
        val builder = mockk<Player.Commands.Builder>()
        val trimmed = mockk<Player.Commands>()
        val offered = mockk<Player.Commands> { every { buildUpon() } returns builder }
        every { builder.remove(Player.COMMAND_SEEK_TO_NEXT) } returns builder
        every { builder.build() } returns trimmed
        every { exo.availableCommands } returns offered
        val added = slot<Player.Listener>()
        every { exo.addListener(capture(added)) } just Runs
        val heard = mutableListOf<Player.Commands>()
        val playing = mutableListOf<Boolean>()
        var eventsFrom: Player? = null
        val listener = object : Player.Listener {
            override fun onAvailableCommandsChanged(availableCommands: Player.Commands) {
                heard += availableCommands
            }
            override fun onIsPlayingChanged(isPlaying: Boolean) {
                playing += isPlaying
            }
            override fun onEvents(player: Player, events: Player.Events) {
                eventsFrom = player
            }
        }

        player.addListener(listener)
        added.captured.onAvailableCommandsChanged(offered)
        added.captured.onIsPlayingChanged(true)
        added.captured.onEvents(exo, mockk())

        assertThat(heard).containsExactly(trimmed)
        assertThat(playing).containsExactly(true)
        assertThat(eventsFrom).isSameInstanceAs(player)
        assertThat(eventsFrom!!.availableCommands).isSameInstanceAs(trimmed)

        player.removeListener(listener)
        verify { exo.removeListener(added.captured) }
    }

    @Test
    fun `the key session's listeners hear no Next either`() {
        val builder = mockk<Player.Commands.Builder>()
        val trimmed = mockk<Player.Commands>()
        val offered = mockk<Player.Commands> { every { buildUpon() } returns builder }
        every { builder.remove(Player.COMMAND_SEEK_TO_NEXT) } returns builder
        every { builder.build() } returns trimmed
        every { exo.availableCommands } returns offered
        val added = slot<Player.Listener>()
        every { exo.addListener(capture(added)) } just Runs
        val heard = mutableListOf<Player.Commands>()
        val listener = object : Player.Listener {
            override fun onAvailableCommandsChanged(availableCommands: Player.Commands) {
                heard += availableCommands
            }
        }
        val keys = KeysOnlySessionPlayer(player)

        // As the MediaSession's own wrapper adds its listener.
        keys.addListener(listener)
        added.captured.onAvailableCommandsChanged(offered)

        assertThat(heard).containsExactly(trimmed)
        keys.removeListener(listener)
        verify { exo.removeListener(added.captured) }
    }

    @Test
    fun `every other command answers as the real player does`() {
        every { exo.isCommandAvailable(Player.COMMAND_STOP) } returns false

        assertThat(player.isCommandAvailable(Player.COMMAND_PLAY_PAUSE)).isTrue()
        assertThat(player.isCommandAvailable(Player.COMMAND_SEEK_TO_PREVIOUS)).isTrue()
        assertThat(player.isCommandAvailable(Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM)).isTrue()
        assertThat(player.isCommandAvailable(Player.COMMAND_STOP)).isFalse()
    }

    @Test
    fun `the key session plays an ended stream from its start and has no Next`() {
        every { exo.playbackState } returns Player.STATE_ENDED
        val keys = KeysOnlySessionPlayer(player)

        // What a headset play key runs, through the session's player.
        Util.handlePlayButtonAction(keys)

        verify { exo.seekTo(0, 0L) }
        verify(exactly = 0) { exo.seekToDefaultPosition(any()) }
        assertThat(keys.isCommandAvailable(Player.COMMAND_SEEK_TO_NEXT)).isFalse()
    }

    @Test
    fun `preparing after an error puts a new source in where playback stopped`() {
        every { exo.playbackState } returns Player.STATE_IDLE
        every { exo.currentPosition } returns 0L

        // Retry's prepare, or Play on the idle player's controller.
        Util.handlePlayButtonAction(player)

        verifyOrder {
            exo.setMediaSource(newSource, 0L)
            exo.prepare()
            exo.play()
        }
    }

    @Test
    fun `a prepared stream keeps its source`() {
        every { exo.playbackState } returns Player.STATE_READY

        player.prepare()

        verify(exactly = 0) { exo.setMediaSource(any(), any<Long>()) }
    }
}
