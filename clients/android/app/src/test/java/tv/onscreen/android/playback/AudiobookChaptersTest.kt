package tv.onscreen.android.playback

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.android.data.model.ChildItem

class AudiobookChaptersTest {

    private fun chapter(id: String, index: Int? = null) =
        ChildItem(id = id, title = id, type = "audiobook_chapter", index = index)

    @Test
    fun `numbered chapters go by number`() {
        // The server lists by (index, title) already; the client sorts anyway
        // so an out-of-order list can't send the book backwards.
        val kids = listOf(chapter("three", 3), chapter("one", 1), chapter("two", 2))
        assertThat(AudiobookChapters.inOrder(kids).map { it.id })
            .containsExactly("one", "two", "three").inOrder()
        assertThat(AudiobookChapters.nextAfter(kids, "one")?.id).isEqualTo("two")
        assertThat(AudiobookChapters.nextAfter(kids, "two")?.id).isEqualTo("three")
    }

    @Test
    fun `unnumbered chapters follow the numbered ones, in the listing's order`() {
        // A chapter the scanner couldn't number still has a place: after the
        // numbered ones, in the order the server lists them — the client
        // doesn't re-sort those.
        val kids = listOf(chapter("Epilogue"), chapter("one", 1), chapter("Afterword"), chapter("two", 2))
        assertThat(AudiobookChapters.inOrder(kids).map { it.id })
            .containsExactly("one", "two", "Epilogue", "Afterword").inOrder()
        assertThat(AudiobookChapters.nextAfter(kids, "two")?.id).isEqualTo("Epilogue")
        assertThat(AudiobookChapters.nextAfter(kids, "Epilogue")?.id).isEqualTo("Afterword")
    }

    @Test
    fun `a wholly unnumbered book plays in listing order`() {
        val kids = listOf(chapter("a"), chapter("b"), chapter("c"))
        assertThat(AudiobookChapters.nextAfter(kids, "a")?.id).isEqualTo("b")
        assertThat(AudiobookChapters.nextAfter(kids, "b")?.id).isEqualTo("c")
    }

    @Test
    fun `the last chapter has no next`() {
        val kids = listOf(chapter("one", 1), chapter("two", 2))
        assertThat(AudiobookChapters.nextAfter(kids, "two")).isNull()
    }

    @Test
    fun `numbering gaps are stepped over`() {
        // Chapter 2's file is missing from the library.
        val kids = listOf(chapter("one", 1), chapter("three", 3), chapter("four", 4))
        assertThat(AudiobookChapters.nextAfter(kids, "one")?.id).isEqualTo("three")
    }

    @Test
    fun `only chapters count, once each`() {
        val kids = listOf(
            chapter("a", 1),
            ChildItem(id = "extra", title = "cover", type = "photo", index = 2),
            chapter("a", 1),
            chapter("b", 3),
        )
        assertThat(AudiobookChapters.nextAfter(kids, "a")?.id).isEqualTo("b")
    }

    @Test
    fun `a chapter no longer listed has no next`() {
        assertThat(AudiobookChapters.nextAfter(listOf(chapter("a")), "gone")).isNull()
        assertThat(AudiobookChapters.nextAfter(emptyList(), "a")).isNull()
    }
}
