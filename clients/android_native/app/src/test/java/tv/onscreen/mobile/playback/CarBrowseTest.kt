package tv.onscreen.mobile.playback

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.ItemFile
import tv.onscreen.mobile.data.model.Library
import tv.onscreen.mobile.data.model.MediaItem
import tv.onscreen.mobile.data.model.SearchResult

class CarBrowseTest {

    /** A small server: one library of each kind (plus a movie library the
     *  car must not show), an artist with an album, an author with a
     *  chaptered book and a single-file book, a podcast with an episode. */
    private class FakeSource : CarBrowse.Source {
        val itemCalls = mutableListOf<Triple<String, String?, Pair<Int, Int>>>()
        var artistsTotal = 3

        override suspend fun libraries() = listOf(
            Library("lib-pod", "Podcasts", "podcast", "", ""),
            Library("lib-mov", "Movies", "movie", "", ""),
            Library("lib-mus", "Music", "music", "", ""),
            Library("lib-book", "Audiobooks", "audiobook", "", ""),
        )

        override suspend fun items(libraryId: String, type: String?, limit: Int, offset: Int): List<MediaItem> {
            itemCalls += Triple(libraryId, type, limit to offset)
            return when {
                libraryId == "lib-mus" && type == "artist" ->
                    (offset until minOf(artistsTotal, offset + limit)).map { mi("artist-$it", "Artist $it", "artist") }
                libraryId == "lib-mus" && type == "album" -> listOf(mi("album-1", "Album One", "album"))
                libraryId == "lib-book" && type == null -> listOf(mi("author-1", "Author", "book_author"))
                libraryId == "lib-pod" && type == null -> listOf(mi("pod-1", "A Podcast", "podcast"))
                else -> emptyList()
            }
        }

        override suspend fun children(id: String): List<ChildItem> = when (id) {
            "artist-0" -> listOf(ci("album-1", "Album One", "album"), ci("mv-1", "A Video", "music_video"))
            // Server order isn't play order: disc 2 listed first.
            "album-1" -> listOf(
                ci("t3", "Three", "track", index = 1, disc = 2),
                ci("t1", "One", "track", index = 1),
                ci("t2", "Two", "track", index = 2),
            )
            "author-1" -> listOf(ci("book-ch", "Chaptered", "audiobook"), ci("book-one", "One File", "audiobook"))
            "book-ch" -> listOf(
                ci("c2", "Chapter 2", "audiobook_chapter", index = 2, offset = 60_000),
                ci("c1", "Chapter 1", "audiobook_chapter", index = 1, watched = true),
                ci("c3", "Chapter 3", "audiobook_chapter", index = 3),
            )
            "book-one" -> emptyList()
            "pod-1" -> listOf(ci("ep-1", "Episode 1", "podcast_episode"))
            else -> emptyList()
        }

        override suspend fun item(id: String): ItemDetail = when (id) {
            "album-1" -> ItemDetail(id, "lib-mus", "Album One", "album", parent_id = "artist-0", poster_path = "/art/album-1.jpg")
            "artist-0" -> ItemDetail(id, "lib-mus", "Artist 0", "artist")
            "book-ch" -> ItemDetail(id, "lib-book", "Chaptered", "audiobook", parent_id = "author-1", poster_path = "/art/book.jpg")
            "book-one" -> ItemDetail(id, "lib-book", "One File", "audiobook", parent_id = "author-1", index = 2,
                view_offset_ms = 90_000, files = listOf(ItemFile("f", "/media/stream/f")))
            "author-1" -> ItemDetail(id, "lib-book", "Author", "book_author")
            "ep-1" -> ItemDetail(id, "lib-pod", "Episode 1", "podcast_episode", parent_id = "pod-1", view_offset_ms = 5_000)
            "pod-1" -> ItemDetail(id, "lib-pod", "A Podcast", "podcast")
            "t2" -> ItemDetail(id, "lib-mus", "Two", "track", parent_id = "album-1")
            "movie-1" -> ItemDetail(id, "lib-mov", "A Movie", "movie")
            else -> throw IllegalArgumentException("no item $id")
        }

        override suspend fun search(query: String, limit: Int) = listOf(
            SearchResult("movie-1", "lib-mov", "A Movie", "movie"),
            SearchResult("t2", "lib-mus", "Two", "track"),
            SearchResult("artist-0", "lib-mus", "Artist 0", "artist"),
        )

        private fun mi(id: String, title: String, type: String) = MediaItem(id = id, title = title, type = type, created_at = "", updated_at = "")
        private fun ci(id: String, title: String, type: String, index: Int? = null, disc: Int? = null, offset: Long = 0, watched: Boolean = false) =
            ChildItem(id = id, title = title, type = type, index = index, disc_number = disc, view_offset_ms = offset, watched = watched)
    }

    private val source = FakeSource()
    private val browse = CarBrowse(source)

    @Test
    fun `root lists the audio libraries only, music first`() = runTest {
        val root = browse.children(CarIds.ROOT, 0, 100)!!
        assertThat(root.map { it.title }).containsExactly("Music", "Audiobooks", "Podcasts").inOrder()
        assertThat(root.all { it.browsable && !it.playable }).isTrue()
    }

    @Test
    fun `a music library offers Artists and Albums`() = runTest {
        val music = browse.children(CarIds.library("lib-mus", "music"), 0, 100)!!
        assertThat(music.map { it.title }).containsExactly("Artists", "Albums").inOrder()
        val albums = browse.children(music[1].id, 0, 100)!!
        assertThat(albums.single().id).isEqualTo(CarIds.container("album", "album-1"))
        assertThat(source.itemCalls.last().second).isEqualTo("album")
    }

    @Test
    fun `listings page through the server in its largest pages`() = runTest {
        source.artistsTotal = 450
        val artists = browse.children(CarIds.listing("lib-mus", "artist"), 0, Int.MAX_VALUE)!!
        assertThat(artists).hasSize(450)
        // 200 + 200 + 50: the third, short page ends it.
        assertThat(source.itemCalls.map { it.third }).containsExactly(200 to 0, 200 to 200, 200 to 400).inOrder()
    }

    @Test
    fun `a car page maps to an offset`() = runTest {
        source.artistsTotal = 30
        val second = browse.children(CarIds.listing("lib-mus", "artist"), 10, 10)!!
        assertThat(second.map { it.title }.first()).isEqualTo("Artist 10")
        assertThat(source.itemCalls.single().third).isEqualTo(10 to 10)
    }

    @Test
    fun `an artist lists albums only, an album its tracks in play order`() = runTest {
        val albums = browse.children(CarIds.container("artist", "artist-0"), 0, 100)!!
        assertThat(albums.map { it.title }).containsExactly("Album One")
        assertThat(albums.single().playable).isTrue()
        val tracks = browse.children(albums.single().id, 0, 100)!!
        assertThat(tracks.map { it.title }).containsExactly("One", "Two", "Three").inOrder()
        assertThat(tracks.first().id).isEqualTo(CarIds.leaf("track", "album-1", "t1"))
    }

    @Test
    fun `tapping a track plays its album from that track`() = runTest {
        val q = browse.queueFor(CarIds.leaf("track", "album-1", "t2"))!!
        assertThat(q.entries.map { it.itemId }).containsExactly("t1", "t2", "t3").inOrder()
        assertThat(q.startIndex).isEqualTo(1)
        assertThat(q.startMs).isEqualTo(0)
        assertThat(q.entries.all { it.parentId == "album-1" && it.album == "Album One" && it.artist == "Artist 0" }).isTrue()
        assertThat(q.entries.first().artworkPath).isEqualTo("/art/album-1.jpg")
    }

    @Test
    fun `playing an album starts at its first track`() = runTest {
        val q = browse.queueFor(CarIds.container("album", "album-1"))!!
        assertThat(q.startIndex).isEqualTo(0)
        assertThat(q.entries).hasSize(3)
    }

    @Test
    fun `a chaptered book resumes at the chapter in progress`() = runTest {
        val q = browse.queueFor(CarIds.container("audiobook", "book-ch"))!!
        assertThat(q.entries.map { it.itemId }).containsExactly("c1", "c2", "c3").inOrder()
        assertThat(q.entries[q.startIndex].itemId).isEqualTo("c2")
        assertThat(q.startMs).isEqualTo(60_000)
        assertThat(q.entries.all { it.type == "audiobook_chapter" && it.parentId == "book-ch" }).isTrue()
    }

    @Test
    fun `tapping a finished chapter plays it from the start`() = runTest {
        val q = browse.queueFor(CarIds.leaf("audiobook_chapter", "book-ch", "c1"))!!
        assertThat(q.entries[q.startIndex].itemId).isEqualTo("c1")
        assertThat(q.startMs).isEqualTo(0)
    }

    @Test
    fun `a single-file book plays itself from its resume point, keeping its place among the author's books`() = runTest {
        val q = browse.queueFor(CarIds.container("audiobook", "book-one"))!!
        val e = q.entries.single()
        assertThat(e.itemId).isEqualTo("book-one")
        assertThat(e.type).isEqualTo("audiobook")
        // The service chains to the author's next book by parent + index.
        assertThat(e.parentId).isEqualTo("author-1")
        assertThat(e.index).isEqualTo(2)
        assertThat(q.startMs).isEqualTo(90_000)
    }

    @Test
    fun `an episode plays from where it was left`() = runTest {
        val q = browse.queueFor(CarIds.leaf("podcast_episode", "pod-1", "ep-1"))!!
        assertThat(q.entries.single().artist).isEqualTo("A Podcast")
        assertThat(q.startMs).isEqualTo(5_000)
    }

    @Test
    fun `containers that are lists are not playable`() = runTest {
        assertThat(browse.queueFor(CarIds.container("artist", "artist-0"))).isNull()
        assertThat(browse.queueFor(CarIds.container("podcast", "pod-1"))).isNull()
        assertThat(browse.queueFor("t2")).isNull()
    }

    @Test
    fun `search keeps audio results and finds a track's album`() = runTest {
        val results = browse.search("two")
        assertThat(results.map { it.title }).containsExactly("Two", "Artist 0").inOrder()
        assertThat(results.first().id).isEqualTo(CarIds.leaf("track", "album-1", "t2"))
    }

    @Test
    fun `play by voice plays the best audio match`() = runTest {
        val q = browse.queueForVoice("two")!!
        assertThat(q.entries[q.startIndex].itemId).isEqualTo("t2")
    }

    @Test
    fun `unknown ids are not part of the tree`() = runTest {
        assertThat(browse.children("nope", 0, 10)).isNull()
        assertThat(browse.children("c:artist", 0, 10)).isNull()
    }

    @Test
    fun `ids round-trip and never collide with server ids`() {
        assertThat(CarIds.parseLeaf(CarIds.leaf("track", "a", "b"))).isEqualTo(Triple("track", "a", "b"))
        assertThat(CarIds.parseContainer(CarIds.container("album", "x"))).isEqualTo("album" to "x")
        assertThat(CarIds.parseLibrary(CarIds.library("l", "music"))).isEqualTo("l" to "music")
        assertThat(CarIds.parseListing(CarIds.listing("l", "artist"))).isEqualTo("l" to "artist")
        assertThat(CarIds.isCarId("3f2b1c9e-0000-4000-8000-000000000000")).isFalse()
        assertThat(CarIds.isCarId("onscreen-item://abc")).isFalse()
        assertThat(CarIds.isCarId("")).isFalse()
    }

    @Test
    fun `durations read as a clock`() {
        assertThat(CarBrowse.clock(65_000)).isEqualTo("1:05")
        assertThat(CarBrowse.clock(3_723_000)).isEqualTo("1:02:03")
    }
}
