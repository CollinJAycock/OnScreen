package tv.onscreen.mobile.playback

import com.google.common.truth.Truth.assertThat
import org.junit.After
import org.junit.Test
import tv.onscreen.mobile.data.model.ChildItem

class AudiobookChaptersTest {

    private fun chapter(id: String, index: Int? = null) =
        ChildItem(id = id, title = id, type = "audiobook_chapter", index = index)

    @After
    fun tearDown() {
        StopAfterItem.disarm("ch-1")
        StopAfterItem.disarm("ch-2")
    }

    @Test
    fun `unnumbered chapters follow the listing`() {
        // The scanner doesn't number chapter files: the book's listing order
        // is the order to play them in.
        val kids = listOf(chapter("a"), chapter("b"), chapter("c"))
        assertThat(AudiobookChapters.nextAfter(kids, "a")?.id).isEqualTo("b")
        assertThat(AudiobookChapters.nextAfter(kids, "b")?.id).isEqualTo("c")
        assertThat(AudiobookChapters.nextAfter(kids, "c")).isNull()
    }

    @Test
    fun `numbered chapters go by number, unnumbered ones after`() {
        val kids = listOf(chapter("x"), chapter("three", 3), chapter("one", 1), chapter("two", 2))
        assertThat(AudiobookChapters.inOrder(kids).map { it.id })
            .containsExactly("one", "two", "three", "x").inOrder()
        assertThat(AudiobookChapters.nextAfter(kids, "three")?.id).isEqualTo("x")
    }

    @Test
    fun `only chapters count, once each`() {
        val kids = listOf(
            chapter("a"),
            ChildItem(id = "extra", title = "cover", type = "photo"),
            chapter("a"),
            chapter("b"),
        )
        assertThat(AudiobookChapters.nextAfter(kids, "a")?.id).isEqualTo("b")
    }

    @Test
    fun `a chapter no longer listed has no next`() {
        assertThat(AudiobookChapters.nextAfter(listOf(chapter("a")), "gone")).isNull()
        assertThat(AudiobookChapters.nextAfter(emptyList(), "a")).isNull()
    }

    @Test
    fun `stop-after-item fires once, for its own item`() {
        StopAfterItem.arm("ch-1")
        assertThat(StopAfterItem.isArmedFor("ch-1")).isTrue()
        assertThat(StopAfterItem.consume("ch-2")).isFalse()
        assertThat(StopAfterItem.consume("ch-1")).isTrue()
        // Consumed: the next chapter chains normally.
        assertThat(StopAfterItem.consume("ch-1")).isFalse()
    }

    @Test
    fun `disarm leaves another item's stop alone`() {
        // A screen torn down after following the queue must not cancel the
        // stop its successor armed.
        StopAfterItem.arm("ch-2")
        StopAfterItem.disarm("ch-1")
        assertThat(StopAfterItem.isArmedFor("ch-2")).isTrue()
        StopAfterItem.disarm("ch-2")
        assertThat(StopAfterItem.isArmedFor("ch-2")).isFalse()
    }
}
