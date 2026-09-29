package tv.onscreen.android.playback

import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.Player
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
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
