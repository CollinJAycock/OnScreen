package tv.onscreen.mobile.playback

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.PlaybackStopEvent
import tv.onscreen.mobile.data.repository.ItemRepository

class MusicQueueTest {

    private fun track(id: String, index: Int? = null) = ChildItem(id = id, title = "t-$id", type = "track", index = index)
    private fun album(id: String, year: Int? = null, index: Int? = null) =
        ChildItem(id = id, title = "a-$id", type = "album", year = year, index = index)

    private val albumListing = listOf(track("t1", 1), track("t2", 2), track("t3", 3), track("t4", 4))

    // ── placeholder uris ────────────────────────────────────────────────

    @Test
    fun `placeholder uri round-trips the item id and carries nothing else`() {
        val uri = MusicQueue.placeholderUri("abc-123")
        assertThat(uri).isEqualTo("onscreen-item://abc-123")
        assertThat(MusicQueue.itemIdFromPlaceholder(uri)).isEqualTo("abc-123")
    }

    @Test
    fun `real stream urls and junk are not placeholders`() {
        assertThat(MusicQueue.itemIdFromPlaceholder("https://srv/media/stream/f1")).isNull()
        assertThat(MusicQueue.itemIdFromPlaceholder("file:///data/x.flac")).isNull()
        assertThat(MusicQueue.itemIdFromPlaceholder("onscreen-item://")).isNull()
        assertThat(MusicQueue.itemIdFromPlaceholder(null)).isNull()
    }

    // ── album expansion ─────────────────────────────────────────────────

    @Test
    fun `only music tracks expand into a queue`() {
        assertThat(MusicQueue.expandsQueue("track")).isTrue()
        assertThat(MusicQueue.expandsQueue("audiobook")).isFalse()
        assertThat(MusicQueue.expandsQueue("episode")).isFalse()
        assertThat(MusicQueue.expandsQueue(null)).isFalse()
    }

    @Test
    fun `queue is the whole album around the chosen track, in album order`() {
        val plan = MusicQueue.planAround("t3", "track", albumListing)!!
        assertThat(plan.before.map { it.id }).containsExactly("t1", "t2").inOrder()
        assertThat(plan.after.map { it.id }).containsExactly("t4")
        assertThat(plan.size).isEqualTo(4)
    }

    @Test
    fun `first and last track put everything on one side`() {
        MusicQueue.planAround("t1", "track", albumListing)!!.let {
            assertThat(it.before).isEmpty()
            assertThat(it.after.map { c -> c.id }).containsExactly("t2", "t3", "t4").inOrder()
        }
        MusicQueue.planAround("t4", "track", albumListing)!!.let {
            assertThat(it.before.map { c -> c.id }).containsExactly("t1", "t2", "t3").inOrder()
            assertThat(it.after).isEmpty()
        }
    }

    @Test
    fun `keeps the server order and skips other child types and duplicates`() {
        // Server sorts by index; a multi-disc album interleaves equal indexes —
        // the queue keeps exactly the order the album page shows.
        val listing = listOf(
            track("d1t1", 1), track("d2t1", 1), ChildItem(id = "bk", title = "booklet", type = "photo"),
            track("d1t2", 2), track("d1t2", 2), track("untracked", null),
        )
        val plan = MusicQueue.planAround("d2t1", "track", listing)!!
        assertThat(plan.before.map { it.id }).containsExactly("d1t1")
        assertThat(plan.after.map { it.id }).containsExactly("d1t2", "untracked").inOrder()
    }

    @Test
    fun `anchor missing from the listing leaves the queue alone`() {
        assertThat(MusicQueue.planAround("gone", "track", albumListing)).isNull()
    }

    // ── continuation at the end of the queue ────────────────────────────

    @Test
    fun `continuation fills in the rest of the album when the queue lacks it`() = runTest {
        var nextAlbumAsked = false
        val c = MusicQueue.continuation("t2", "track", "alb", albumListing, queuedIds = setOf("t2")) {
            nextAlbumAsked = true
            null
        }!!
        assertThat(c.parentId).isEqualTo("alb")
        assertThat(c.tracks.map { it.id }).containsExactly("t3", "t4").inOrder()
        assertThat(nextAlbumAsked).isFalse()
    }

    @Test
    fun `continuation moves on to the next album once this one is queued`() = runTest {
        val queued = albumListing.map { it.id }.toSet()
        val c = MusicQueue.continuation("t4", "track", "alb", albumListing, queued) {
            "alb2" to listOf(track("b1", 1), track("b2", 2))
        }!!
        assertThat(c.parentId).isEqualTo("alb2")
        assertThat(c.tracks.map { it.id }).containsExactly("b1", "b2").inOrder()
    }

    @Test
    fun `continuation never re-queues what is already queued`() = runTest {
        val c = MusicQueue.continuation("t4", "track", "alb", albumListing, setOf("t1", "t2", "t3", "t4", "b1")) {
            "alb2" to listOf(track("b1", 1), track("b2", 2))
        }!!
        assertThat(c.tracks.map { it.id }).containsExactly("b2")
    }

    @Test
    fun `continuation is null at the end of the discography`() = runTest {
        val queued = albumListing.map { it.id }.toSet()
        assertThat(MusicQueue.continuation("t4", "track", "alb", albumListing, queued) { null }).isNull()
        assertThat(
            MusicQueue.continuation("t4", "track", "alb", albumListing, queued) { "empty" to emptyList() },
        ).isNull()
    }

    // ── next album lookup (shared with NextSiblingResolver) ─────────────

    @Test
    fun `nextContainer picks the artist's next album by year then index`() = runTest {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getItem("alb-a") } returns ItemDetail(
            id = "alb-a", library_id = "lib", title = "A", type = "album", parent_id = "artist",
        )
        coEvery { repo.getChildren("artist") } returns listOf(
            album("alb-c", year = 2024), album("alb-a", year = 2020), album("alb-b", year = 2022),
        )
        val next = NextSiblingResolver(repo).nextContainer("alb-a", "track")
        assertThat(next?.id).isEqualTo("alb-b")
    }

    @Test
    fun `nextContainer is null for types without containers and on lookup failure`() = runTest {
        val repo = mockk<ItemRepository>()
        assertThat(NextSiblingResolver(repo).nextContainer("x", "audiobook")).isNull()
        coVerify(exactly = 0) { repo.getItem(any()) }
        coEvery { repo.getItem("alb") } throws RuntimeException("offline")
        assertThat(NextSiblingResolver(repo).nextContainer("alb", "track")).isNull()
    }

    // ── admin stop targeting for background audio ───────────────────────

    @Test
    fun `background audio obeys untargeted and name-targeted stops for its item`() {
        fun targets(ev: PlaybackStopEvent, current: String?) =
            ev.targets(current, sessionId = null, clientName = PlaybackService.STREAM_CLIENT_NAME)

        // Untargeted stop of the playing track.
        assertThat(targets(PlaybackStopEvent(item_id = "t1"), "t1")).isTrue()
        // Aimed at this phone's direct-play stream (named by its User-Agent).
        assertThat(targets(PlaybackStopEvent(item_id = "t1", client_name = "OnScreenPhone"), "t1")).isTrue()
        // Another device playing the same track.
        assertThat(targets(PlaybackStopEvent(item_id = "t1", client_name = "Chrome"), "t1")).isFalse()
        // Someone's transcode session — background audio never has one.
        assertThat(targets(PlaybackStopEvent(item_id = "t1", session_id = "s1", client_name = "OnScreenWeb"), "t1"))
            .isFalse()
        // A different track, or nothing playing.
        assertThat(targets(PlaybackStopEvent(item_id = "t2"), "t1")).isFalse()
        assertThat(targets(PlaybackStopEvent(item_id = "t1"), null)).isFalse()
    }

    // ── Play on an album / artist page ──────────────────────────────────

    @Test
    fun `only albums and artists start from a container`() {
        assertThat(MusicQueue.startsFromContainer("album")).isTrue()
        assertThat(MusicQueue.startsFromContainer("artist")).isTrue()
        assertThat(MusicQueue.startsFromContainer("track")).isFalse()
        assertThat(MusicQueue.startsFromContainer("show")).isFalse()
        assertThat(MusicQueue.startsFromContainer(null)).isFalse()
    }

    @Test
    fun `album play starts at its first track in album order`() = runTest {
        val listing = listOf(
            ChildItem(id = "art", title = "cover", type = "photo"),
            track("t1", 1), track("t2", 2),
        )
        val start = MusicQueue.playStart("album", listing) { error("no fetch for an album") }
        assertThat(start).isEqualTo("t1")
        assertThat(MusicQueue.playStart("album", emptyList()) { emptyList() }).isNull()
    }

    @Test
    fun `artist play starts the first album by year - the order the queue continues in`() = runTest {
        val fetched = mutableListOf<String>()
        val albums = listOf(album("late", year = 2024), album("undated"), album("early", year = 2019))
        val start = MusicQueue.playStart("artist", albums) { id ->
            fetched += id
            when (id) {
                "early" -> listOf(track("e1", 1), track("e2", 2))
                else -> error("only the first album is needed")
            }
        }
        assertThat(start).isEqualTo("e1")
        assertThat(fetched).containsExactly("early")
    }

    @Test
    fun `artist play skips an empty album and is null with none playable`() = runTest {
        val albums = listOf(album("a", year = 2001), album("b", year = 2002))
        val start = MusicQueue.playStart("artist", albums) { id ->
            if (id == "a") emptyList() else listOf(track("b1", 1))
        }
        assertThat(start).isEqualTo("b1")
        assertThat(MusicQueue.playStart("artist", albums) { emptyList() }).isNull()
        assertThat(MusicQueue.playStart("movie", albums) { emptyList() }).isNull()
    }
}
