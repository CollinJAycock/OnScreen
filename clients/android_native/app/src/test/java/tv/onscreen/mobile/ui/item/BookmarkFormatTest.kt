package tv.onscreen.mobile.ui.item

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.Bookmark
import tv.onscreen.mobile.data.model.Chapter

class BookmarkFormatTest {

    private val numbered: (Int) -> String = { "Chapter $it" }

    private fun bm(id: String, itemId: String, pos: Long, title: String = "", index: Int? = null, note: String = "") =
        Bookmark(id = id, item_id = itemId, item_title = title, item_index = index, position_ms = pos, note = note)

    @Test
    fun `timestamp reads like the chapter list`() {
        assertThat(BookmarkFormat.timestamp(0)).isEqualTo("0:00")
        assertThat(BookmarkFormat.timestamp(83_000)).isEqualTo("1:23")
        assertThat(BookmarkFormat.timestamp(5_025_000)).isEqualTo("1:23:45")
        assertThat(BookmarkFormat.timestamp(-5)).isEqualTo("0:00")
    }

    @Test
    fun `a single-file book names the embedded chapter at the bookmark`() {
        val chapters = listOf(Chapter("Opening", 0, 60_000), Chapter("", 60_000, 120_000))
        val book = "book-1"
        assertThat(BookmarkFormat.chapterLabel(bm("1", book, 30_000), book, chapters, numbered))
            .isEqualTo("Opening")
        // Untitled mark: numbered by position in the file.
        assertThat(BookmarkFormat.chapterLabel(bm("2", book, 90_000), book, chapters, numbered))
            .isEqualTo("Chapter 2")
        // No marks at all: nothing to name it by (the row leads with the time).
        assertThat(BookmarkFormat.chapterLabel(bm("3", book, 90_000, title = "Book"), book, emptyList(), numbered))
            .isNull()
    }

    @Test
    fun `a multi-file book names the chapter file`() {
        assertThat(BookmarkFormat.chapterLabel(bm("1", "ch-4", 5_000, title = "The Storm", index = 4), "book-1", emptyList(), numbered))
            .isEqualTo("The Storm")
        assertThat(BookmarkFormat.chapterLabel(bm("2", "ch-4", 5_000, title = " ", index = 4), "book-1", emptyList(), numbered))
            .isEqualTo("Chapter 4")
        assertThat(BookmarkFormat.chapterLabel(bm("3", "ch-4", 5_000), "book-1", emptyList(), numbered))
            .isNull()
    }

    @Test
    fun `multi-file bookmarks follow the book's chapter order, then position`() {
        // Unnumbered chapter files: the server can only sort by position,
        // interleaving chapters. The book's listing puts them right.
        val server = listOf(
            bm("a", "ch-2", 1_000),
            bm("b", "ch-1", 2_000),
            bm("c", "ch-2", 500),
            bm("d", "ch-1", 9_000),
        )
        val ordered = BookmarkFormat.inListeningOrder(server, "book-1", listOf("ch-1", "ch-2"))
        assertThat(ordered.map { it.id }).containsExactly("b", "d", "c", "a").inOrder()
    }

    @Test
    fun `a chapter missing from the listing keeps the server's order, last`() {
        val server = listOf(bm("x", "gone", 9_000), bm("y", "gone", 1_000), bm("a", "ch-1", 5_000))
        val ordered = BookmarkFormat.inListeningOrder(server, "book-1", listOf("ch-1"))
        assertThat(ordered.map { it.id }).containsExactly("a", "x", "y").inOrder()
    }

    @Test
    fun `without a chapter listing the server's order stands`() {
        // A single-file book: already by position.
        val server = listOf(bm("1", "book-1", 1_000), bm("2", "book-1", 2_000))
        assertThat(BookmarkFormat.inListeningOrder(server, "book-1", emptyList())).isEqualTo(server)
    }

    @Test
    fun `notes are limited and counted as the server counts them`() {
        val long = "a".repeat(600)
        assertThat(BookmarkFormat.limitNote(long)).hasLength(BookmarkFormat.NOTE_MAX)
        assertThat(BookmarkFormat.limitNote("short")).isEqualTo("short")
        // Code points, not UTF-16 units: an emoji is one character, and the
        // cut never splits one in half.
        val emoji = "😀".repeat(501)
        val cut = BookmarkFormat.limitNote(emoji)
        assertThat(cut.codePointCount(0, cut.length)).isEqualTo(500)
        assertThat(BookmarkFormat.noteLength("  😀 x  ")).isEqualTo(3)
    }
}
