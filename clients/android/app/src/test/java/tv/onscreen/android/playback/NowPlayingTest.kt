package tv.onscreen.android.playback

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.repository.ItemRepository

class NowPlayingTest {

    private fun item(id: String, type: String, title: String, parent: String? = null, original: String? = null) =
        ItemDetail(id = id, library_id = "lib", title = title, type = type, parent_id = parent, original_title = original)

    private val artist = item("artist-1", "artist", "AC/DC")
    private val album = item("album-1", "album", "High Voltage", parent = "artist-1")
    private val track = item("track-1", "track", "It's a Long Way to the Top", parent = "album-1")

    @Test
    fun `a track names its album and artist`() {
        val np = NowPlaying.of(track, album, artist)
        assertThat(np).isEqualTo(
            NowPlaying("It's a Long Way to the Top", artist = "AC/DC", album = "High Voltage", mediaId = "track-1"),
        )
    }

    @Test
    fun `a chapter names its book and author, a book its author`() {
        val author = item("author-1", "book_author", "George R. R. Martin")
        val book = item("book-1", "audiobook", "A Clash of Kings", parent = "author-1", original = "George R. R. Martin")
        val chapter = item("ch-1", "audiobook_chapter", "40 - Daenerys III", parent = "book-1")

        assertThat(NowPlaying.of(chapter, book, author).album).isEqualTo("A Clash of Kings")
        assertThat(NowPlaying.of(chapter, book, author).artist).isEqualTo("George R. R. Martin")
        // No author item above the book: its tagged author.
        assertThat(NowPlaying.of(chapter, book, null).artist).isEqualTo("George R. R. Martin")
        assertThat(NowPlaying.of(book, author).artist).isEqualTo("George R. R. Martin")
    }

    @Test
    fun `missing parents fall back to the tagged artist, else leave it out`() {
        assertThat(NowPlaying.of(track.copy(original_title = "AC/DC")).artist).isEqualTo("AC/DC")
        val bare = NowPlaying.of(track)
        assertThat(bare.artist).isNull()
        assertThat(bare.album).isNull()
        assertThat(NowPlaying.of(item("m", "movie", "Heat"))).isEqualTo(NowPlaying("Heat", mediaId = "m"))
    }

    @Test
    fun `media metadata carries the names over what the stream had`() {
        val base = androidx.media3.common.MediaMetadata.Builder().setTrackNumber(3).setArtist("tag artist").build()
        val md = NowPlaying("Title", artist = "Artist", album = "Album").toMediaMetadata(base)
        assertThat(md.title.toString()).isEqualTo("Title")
        assertThat(md.displayTitle.toString()).isEqualTo("Title")
        assertThat(md.artist.toString()).isEqualTo("Artist")
        assertThat(md.albumTitle.toString()).isEqualTo("Album")
        assertThat(md.trackNumber).isEqualTo(3)
        // No artist of our own: the stream's stays.
        assertThat(NowPlaying("Title").toMediaMetadata(base).artist.toString()).isEqualTo("tag artist")
    }

    @Test
    fun `resolve looks the parents up, and a failed lookup only drops that name`() = runTest {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getItem("album-1") } returns album
        coEvery { repo.getItem("artist-1") } throws RuntimeException("offline")

        val np = NowPlaying.resolve(repo, track)

        assertThat(np.album).isEqualTo("High Voltage")
        assertThat(np.artist).isNull()
        assertThat(np.title).isEqualTo("It's a Long Way to the Top")
    }

    @Test
    fun `resolve doesn't look anything up for video`() = runTest {
        val repo = mockk<ItemRepository>() // any call would throw
        val np = NowPlaying.resolve(repo, item("ep-1", "episode", "Pilot", parent = "season-1"))
        assertThat(np).isEqualTo(NowPlaying("Pilot", mediaId = "ep-1"))
    }
}
