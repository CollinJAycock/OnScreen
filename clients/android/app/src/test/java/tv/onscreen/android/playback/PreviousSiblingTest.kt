package tv.onscreen.android.playback

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Test
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.repository.ItemRepository

/** Previous for background audio (a remote's MEDIA_PREVIOUS on the service's
 *  session): the item before the playing one, never across containers. */
class PreviousSiblingTest {

    @After
    fun tearDown() = PlayQueue.clear()

    private fun track(id: String, index: Int?, disc: Int? = null) =
        ChildItem(id = id, title = id, type = "track", index = index, disc_number = disc)

    private fun chapter(id: String, index: Int? = null) =
        ChildItem(id = id, title = id, type = "audiobook_chapter", index = index)

    @Test
    fun `the previous track in the album, stepping over a gap`() {
        val kids = listOf(track("t1", 1), track("t2", 2), track("t5", 5))
        assertThat(NextSiblingResolver.previousInContainer(kids, "t5", "track", 5)?.id).isEqualTo("t2")
        assertThat(NextSiblingResolver.previousInContainer(kids, "t2", "track", 2)?.id).isEqualTo("t1")
        assertThat(NextSiblingResolver.previousInContainer(kids, "t1", "track", 1)).isNull()
    }

    @Test
    fun `disc 2 track 1 goes back to the last track of disc 1`() {
        val kids = listOf(track("a1", 1, 1), track("a9", 9, 1), track("b1", 1, 2), track("b2", 2, 2))
        assertThat(NextSiblingResolver.previousInContainer(kids, "b1", "track", 1)?.id).isEqualTo("a9")
        assertThat(NextSiblingResolver.previousInContainer(kids, "b2", "track", 2)?.id).isEqualTo("b1")
    }

    @Test
    fun `other types and unnumbered rows are skipped`() {
        val kids = listOf(
            ChildItem(id = "img", title = "img", type = "photo", index = 1),
            track("loose", null),
            track("t2", 2),
        )
        assertThat(NextSiblingResolver.previousInContainer(kids, "t2", "track", 2)).isNull()
    }

    @Test
    fun `a chapter goes back to the book's previous chapter, not past the first`() = runTest {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("book") } returns listOf(chapter("c2", 2), chapter("c1", 1), chapter("c3", 3))
        val r = NextSiblingResolver(repo)
        assertThat(r.resolvePrevious("c3", "audiobook_chapter", "book", 3)?.id).isEqualTo("c2")
        assertThat(r.resolvePrevious("c1", "audiobook_chapter", "book", 1)).isNull()
        assertThat(AudiobookChapters.previousBefore(listOf(chapter("x")), "missing")).isNull()
    }

    @Test
    fun `an instant mix goes back within the mix`() = runTest {
        PlayQueue.set(listOf(track("a", 3), track("x", 1), track("b", 7)))
        val repo = mockk<ItemRepository>()
        val r = NextSiblingResolver(repo)
        assertThat(r.resolvePrevious("b", "track", "album-b", 7)?.id).isEqualTo("x")
        assertThat(r.resolvePrevious("a", "track", "album-a", 3)).isNull()
    }

    @Test
    fun `an album track resolves through the container listing`() = runTest {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("album") } returns listOf(track("t1", 1), track("t2", 2))
        val r = NextSiblingResolver(repo)
        assertThat(r.resolvePrevious("t2", "track", "album", 2)?.id).isEqualTo("t1")
        // No parent or index: nothing to go back to.
        assertThat(r.resolvePrevious("t2", "track", null, 2)).isNull()
        assertThat(r.resolvePrevious("t2", "track", "album", null)).isNull()
    }

    @Test
    fun `a failed listing resolves to null`() = runTest {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren(any()) } throws RuntimeException("offline")
        assertThat(NextSiblingResolver(repo).resolvePrevious("t2", "track", "album", 2)).isNull()
    }
}
