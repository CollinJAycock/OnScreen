package tv.onscreen.android.playback

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Test
import tv.onscreen.android.data.model.ArtistAppearance
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.MixTrack
import tv.onscreen.android.data.model.SimilarArtist
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.MusicRepository

/** Instant mixes (v2.6): the mix as a play queue, and the music discovery
 *  rows the artist page lists. */
class PlayQueueTest {

    @After
    fun tearDown() = PlayQueue.clear()

    private fun track(id: String, index: Int? = null) = ChildItem(id = id, title = id, type = "track", index = index)

    @Test
    fun `a mix's track is followed by the next in the mix, not its album`() = runTest {
        PlayQueue.set(listOf(track("a"), track("x"), track("b")))
        val repo = mockk<ItemRepository>()

        val next = NextSiblingResolver(repo).resolve("a", "track", "album-a", 1)

        assertThat(next?.id).isEqualTo("x")
        coVerify(exactly = 0) { repo.getChildren(any()) }
    }

    @Test
    fun `the mix ends after its last track`() = runTest {
        PlayQueue.set(listOf(track("a"), track("b")))
        val repo = mockk<ItemRepository>()

        assertThat(NextSiblingResolver(repo).resolve("b", "track", "album-b", 3)).isNull()
        coVerify(exactly = 0) { repo.getChildren(any()) }
    }

    @Test
    fun `an unnumbered mix track still has a next one`() = runTest {
        PlayQueue.set(listOf(track("a"), track("b")))

        assertThat(NextSiblingResolver(mockk()).resolve("a", "track", null, null)?.id).isEqualTo("b")
    }

    @Test
    fun `an item outside the mix walks its album as before`() = runTest {
        PlayQueue.set(listOf(track("a"), track("b")))
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("album-1") } returns listOf(track("t1", 1), track("t2", 2))

        assertThat(NextSiblingResolver(repo).resolve("t1", "track", "album-1", 1)?.id).isEqualTo("t2")
    }

    @Test
    fun `playing something outside the mix ends it`() {
        PlayQueue.set(listOf(track("a"), track("b")))

        PlayQueue.retainFor("a")
        assertThat(PlayQueue.contains("b")).isTrue()

        PlayQueue.retainFor("elsewhere")
        assertThat(PlayQueue.contains("a")).isFalse()
        assertThat(PlayQueue.nextAfter("a")).isNull()
    }

    @Test
    fun `mix tracks become track rows in play order, duplicates dropped`() {
        val rows = MusicRepository.mixRows(
            listOf(
                MixTrack(id = "t1", title = "One", duration_ms = 1000L, album_poster_path = "a/p.jpg"),
                MixTrack(id = "t2", title = "Two"),
                MixTrack(id = "t1", title = "One again"),
            ),
        )

        assertThat(rows.map { it.id }).containsExactly("t1", "t2").inOrder()
        assertThat(rows[0].type).isEqualTo("track")
        assertThat(rows[0].poster_path).isEqualTo("a/p.jpg")
        assertThat(rows[0].duration_ms).isEqualTo(1000L)
    }

    @Test
    fun `appearances become one row per album, in the server's order`() {
        val rows = MusicRepository.appearanceAlbums(
            listOf(
                ArtistAppearance(id = "tr1", album_id = "new", album_title = "New", album_year = 2024, album_artist_title = "Them"),
                ArtistAppearance(id = "tr2", album_id = "new", album_title = "New", album_year = 2024, album_artist_title = "Them"),
                ArtistAppearance(id = "tr3", album_id = "comp", album_title = "Hits", album_year = 1999),
            ),
        )

        assertThat(rows.map { it.id }).containsExactly("new", "comp").inOrder()
        assertThat(rows.map { it.title }).containsExactly("New · Them", "Hits").inOrder()
        assertThat(rows.map { it.type }).containsExactly("album", "album")
        assertThat(rows[0].year).isEqualTo(2024)
    }

    @Test
    fun `similar artists become artist rows`() {
        val rows = MusicRepository.similarRows(
            listOf(SimilarArtist(id = "a2", title = "Like", poster_path = "p.jpg", score = 0.8)),
        )

        assertThat(rows.single().type).isEqualTo("artist")
        assertThat(rows.single().title).isEqualTo("Like")
        assertThat(rows.single().poster_path).isEqualTo("p.jpg")
    }
}
