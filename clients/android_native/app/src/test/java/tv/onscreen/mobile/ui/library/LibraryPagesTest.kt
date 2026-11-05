package tv.onscreen.mobile.ui.library

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.LibraryLetter
import tv.onscreen.mobile.data.model.MediaItem

class LibraryPagesTest {

    private fun item(id: String) = MediaItem(id = id, title = id, type = "movie", created_at = "0", updated_at = "0")

    private fun rows(from: Int, until: Int) = (from until until).map { item("r$it") }

    @Test
    fun `rows read through their page - unloaded rows are null`() {
        val pages = LibraryPages(pageSize = 10)
            .withPage(0, rows(0, 10), total = 95)
            .withPage(7, rows(70, 80), total = 95)
        assertThat(pages.total).isEqualTo(95)
        assertThat(pages[3]!!.id).isEqualTo("r3")
        assertThat(pages[75]!!.id).isEqualTo("r75")
        assertThat(pages[10]).isNull()
        assertThat(pages[-1]).isNull()
        assertThat(pages[95]).isNull()
        assertThat(pages.loadedItems.map { it.id }).isEqualTo((rows(0, 10) + rows(70, 80)).map { it.id })
    }

    @Test
    fun `pagesFor clamps to the listing`() {
        val pages = LibraryPages(pageSize = 10).withPage(0, rows(0, 10), total = 95)
        assertThat(pages.pagesFor(-30, 5)).isEqualTo(0..0)
        assertThat(pages.pagesFor(42, 61)).isEqualTo(4..6)
        assertThat(pages.pagesFor(80, 500)).isEqualTo(8..9)
        assertThat(LibraryPages().pagesFor(0, 10).isEmpty()).isTrue()
    }

    @Test
    fun `a short page ends the listing where it stops`() {
        // Total said 95 but the page at 50 came back with 3: nothing past 53.
        val pages = LibraryPages(pageSize = 10)
            .withPage(0, rows(0, 10), total = 95)
            .withPage(5, rows(50, 53), total = 95)
        assertThat(pages.total).isEqualTo(53)
        assertThat(pages.pagesFor(0, 1000)).isEqualTo(0..5)
    }

    @Test
    fun `the newest total wins`() {
        val pages = LibraryPages(pageSize = 10)
            .withPage(0, rows(0, 10), total = 95)
            .withPage(1, rows(10, 20), total = 120)
        assertThat(pages.total).isEqualTo(120)
    }

    @Test
    fun `mapItems and plusMissingFrom keep positions`() {
        val a = LibraryPages(pageSize = 10).withPage(0, rows(0, 10), total = 30)
        val b = LibraryPages(pageSize = 10).withPage(2, rows(20, 30), total = 30)
        val merged = a.mapItems { if (it.id == "r1") it.copy(title = "one") else it }.plusMissingFrom(b)
        assertThat(merged[1]!!.title).isEqualTo("one")
        assertThat(merged[25]!!.id).isEqualTo("r25")
        assertThat(merged.find("r25")).isNotNull()
        assertThat(merged.loadedPages).containsExactly(0, 2)
    }

    // ── Rail geometry ─────────────────────────────────────────────────────

    @Test
    fun `a touch picks the letter whose share of the rail it is in`() {
        // 4 letters over 400px: 0-99 → 0, 100-199 → 1, …
        assertThat(letterIndexAt(0f, 400, 4)).isEqualTo(0)
        assertThat(letterIndexAt(150f, 400, 4)).isEqualTo(1)
        assertThat(letterIndexAt(399f, 400, 4)).isEqualTo(3)
        // Dragging past either end holds the first / last letter.
        assertThat(letterIndexAt(-50f, 400, 4)).isEqualTo(0)
        assertThat(letterIndexAt(900f, 400, 4)).isEqualTo(3)
        assertThat(letterIndexAt(10f, 400, 0)).isEqualTo(-1)
    }

    @Test
    fun `a short rail draws every n-th letter`() {
        assertThat(visibleLetterIndices(5, 10)).containsExactly(0, 1, 2, 3, 4).inOrder()
        // 27 letters (# + A-Z) in 14 slots: every other one.
        assertThat(visibleLetterIndices(27, 14)).isEqualTo((0 until 27 step 2).toList())
        assertThat(visibleLetterIndices(27, 9)).isEqualTo((0 until 27 step 3).toList())
        assertThat(visibleLetterIndices(27, 0)).containsExactly(0)
        assertThat(visibleLetterIndices(0, 10)).isEmpty()
    }

    @Test
    fun `the highlighted letter is the section the top row is in`() {
        val letters = listOf(LibraryLetter("#", 2, 0), LibraryLetter("A", 5, 2), LibraryLetter("C", 3, 7))
        assertThat(letterAtRow(letters, 0)).isEqualTo("#")
        assertThat(letterAtRow(letters, 2)).isEqualTo("A")
        assertThat(letterAtRow(letters, 6)).isEqualTo("A")
        assertThat(letterAtRow(letters, 9)).isEqualTo("C")
        assertThat(letterAtRow(emptyList(), 3)).isNull()
    }
}
