package tv.onscreen.android.playback

import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.Player
import androidx.media3.common.util.Util
import com.google.common.truth.Truth.assertThat
import io.mockk.Runs
import io.mockk.every
import io.mockk.just
import io.mockk.mockk
import io.mockk.slot
import io.mockk.verify
import org.junit.Test

class SessionPlayerTest {

    private val inner = mockk<Player>(relaxed = true).apply {
        every { currentPosition } returns 8_000L
        every { contentPosition } returns 8_000L
        every { bufferedPosition } returns 20_000L
        every { contentBufferedPosition } returns 20_000L
        every { duration } returns 25_000L
        every { contentDuration } returns 25_000L
        every { mediaMetadata } returns MediaMetadata.Builder().setTrackNumber(1).build()
        every { currentMediaItem } returns MediaItem.fromUri("http://srv/p.m3u8")
    }

    private var offset = 292_000L
    private var itemDuration: Long? = 317_230L
    private var names: NowPlaying? = NowPlaying("Track", artist = "Artist", album = "Album", mediaId = "item-1")

    private val player = SessionPlayer(inner, { offset }, { itemDuration }, { names })

    @Test
    fun `a resumed session shows the item's own time`() {
        assertThat(player.currentPosition).isEqualTo(300_000L)
        assertThat(player.contentPosition).isEqualTo(300_000L)
        assertThat(player.bufferedPosition).isEqualTo(312_000L)
        assertThat(player.duration).isEqualTo(317_230L)
        assertThat(player.contentDuration).isEqualTo(317_230L)
    }

    @Test
    fun `without the item's length, the remaining part plus the offset`() {
        itemDuration = null
        assertThat(player.duration).isEqualTo(317_000L)
        every { inner.duration } returns C.TIME_UNSET
        assertThat(player.duration).isEqualTo(C.TIME_UNSET)
    }

    @Test
    fun `direct play passes straight through`() {
        offset = 0L
        itemDuration = null
        assertThat(player.currentPosition).isEqualTo(8_000L)
        assertThat(player.duration).isEqualTo(25_000L)
        player.seekTo(12_000L)
        verify { inner.seekTo(12_000L) }
    }

    @Test
    fun `a session from the start still shows the whole length`() {
        // A transcode opened at 0 only knows what it has produced so far.
        offset = 0L
        assertThat(player.currentPosition).isEqualTo(8_000L)
        assertThat(player.duration).isEqualTo(317_230L)
    }

    @Test
    fun `a seek in item time lands in player time, never before the session's start`() {
        player.seekTo(300_000L)
        verify { inner.seekTo(8_000L) }
        player.seekTo(0, 100_000L)
        verify { inner.seekTo(0, 0L) }
    }

    @Test
    fun `Play once the item has ended replays it from the session's start`() {
        // Util.handlePlayButtonAction, as the system's media controls send
        // Play: the player's own default of a session still being written
        // trails its live edge.
        every { inner.playbackState } returns Player.STATE_ENDED
        every { inner.isCommandAvailable(any()) } returns true
        Util.handlePlayButtonAction(player)
        verify { inner.seekTo(0L) }
        verify { inner.play() }
        player.seekToDefaultPosition(0)
        verify { inner.seekTo(0, 0L) }
        verify(exactly = 0) { inner.seekToDefaultPosition() }
        verify(exactly = 0) { inner.seekToDefaultPosition(any()) }
    }

    @Test
    fun `no Next without a next item`() {
        // The player offers one for a session still being written (a live
        // stream to it), to its default position.
        every { inner.hasNextMediaItem() } returns false
        every { inner.isCommandAvailable(any()) } returns true
        val builder = mockk<Player.Commands.Builder>()
        val trimmed = mockk<Player.Commands>()
        val offered = mockk<Player.Commands> {
            every { contains(Player.COMMAND_SEEK_TO_NEXT) } returns true
            every { buildUpon() } returns builder
        }
        every { builder.remove(Player.COMMAND_SEEK_TO_NEXT) } returns builder
        every { builder.build() } returns trimmed
        every { inner.availableCommands } returns offered

        assertThat(player.isCommandAvailable(Player.COMMAND_SEEK_TO_NEXT)).isFalse()
        assertThat(player.isCommandAvailable(Player.COMMAND_PLAY_PAUSE)).isTrue()
        assertThat(player.availableCommands).isSameInstanceAs(trimmed)
        player.seekToNext()
        verify(exactly = 0) { inner.seekToNext() }
    }

    @Test
    fun `Next to a next item passes through`() {
        every { inner.hasNextMediaItem() } returns true
        every { inner.isCommandAvailable(any()) } returns true
        val offered = mockk<Player.Commands>()
        every { inner.availableCommands } returns offered

        assertThat(player.isCommandAvailable(Player.COMMAND_SEEK_TO_NEXT)).isTrue()
        assertThat(player.availableCommands).isSameInstanceAs(offered)
        player.seekToNext()
        verify { inner.seekToNext() }
    }

    @Test
    fun `listeners hear the session's commands, without Next`() {
        // The media session takes the commands the player's event carries,
        // not availableCommands: the event's own still offered Next.
        every { inner.hasNextMediaItem() } returns false
        every { inner.isCommandAvailable(any()) } returns true
        val builder = mockk<Player.Commands.Builder>()
        val trimmed = mockk<Player.Commands>()
        val offered = mockk<Player.Commands> {
            every { contains(Player.COMMAND_SEEK_TO_NEXT) } returns true
            every { buildUpon() } returns builder
        }
        every { builder.remove(Player.COMMAND_SEEK_TO_NEXT) } returns builder
        every { builder.build() } returns trimmed
        every { inner.availableCommands } returns offered
        val added = slot<Player.Listener>()
        every { inner.addListener(capture(added)) } just Runs
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
        added.captured.onEvents(inner, mockk())

        assertThat(heard).containsExactly(trimmed)
        assertThat(playing).containsExactly(true)
        assertThat(eventsFrom).isSameInstanceAs(player)
        assertThat(eventsFrom!!.availableCommands).isSameInstanceAs(trimmed)

        player.removeListener(listener)
        verify { inner.removeListener(added.captured) }
    }

    @Test
    fun `listeners hear a next item's Next`() {
        every { inner.hasNextMediaItem() } returns true
        every { inner.isCommandAvailable(any()) } returns true
        val offered = mockk<Player.Commands>()
        every { inner.availableCommands } returns offered
        val added = slot<Player.Listener>()
        every { inner.addListener(capture(added)) } just Runs
        val heard = mutableListOf<Player.Commands>()

        player.addListener(object : Player.Listener {
            override fun onAvailableCommandsChanged(availableCommands: Player.Commands) {
                heard += availableCommands
            }
        })
        added.captured.onAvailableCommandsChanged(offered)

        assertThat(heard).containsExactly(offered)
    }

    @Test
    fun `the session names the item`() {
        val md = player.mediaMetadata
        assertThat(md.title.toString()).isEqualTo("Track")
        assertThat(md.artist.toString()).isEqualTo("Artist")
        assertThat(md.trackNumber).isEqualTo(1)
        val item = player.currentMediaItem!!
        assertThat(item.mediaId).isEqualTo("item-1")
        assertThat(item.mediaMetadata.albumTitle.toString()).isEqualTo("Album")

        names = null
        assertThat(player.mediaMetadata.title).isNull()
    }
}
