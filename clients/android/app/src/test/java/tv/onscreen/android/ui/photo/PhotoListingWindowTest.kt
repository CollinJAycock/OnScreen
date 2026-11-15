package tv.onscreen.android.ui.photo

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.runBlocking
import org.junit.Test

/** The viewer's window onto a library listing: a page at a time around the
 *  photo on screen, stepping over non-photos and wrapping at the ends —
 *  opened at the row the grid passed, or (from anywhere else) at the row the
 *  server reports. */
class PhotoListingWindowTest {

    /** A listing of [total] rows, "p<i>" photos except the rows in [others];
     *  counts the page fetches. */
    private class Listing(val total: Int, val others: Set<Int> = emptySet()) {
        val fetched = mutableListOf<Int>()
        suspend fun fetch(offset: Int, limit: Int): Pair<List<PhotoListingWindow.Entry>, Int> {
            fetched += offset
            val rows = (offset until minOf(offset + limit, total)).map { i ->
                PhotoListingWindow.Entry("p$i", isPhoto = i !in others)
            }
            return rows to total
        }
    }

    private fun window(listing: Listing, start: Int, pageSize: Int = 10, maxPages: Int = 3) =
        PhotoListingWindow(start, pageSize, maxPages) { off, limit -> listing.fetch(off, limit) }

    @Test
    fun `it starts on the clicked photo's row and loads only its page`() = runBlocking<Unit> {
        val listing = Listing(total = 10_000)
        val w = window(listing, start = 5_432)

        assertThat(w.start("p5432")).isTrue()

        assertThat(w.index).isEqualTo(5_432)
        assertThat(w.total).isEqualTo(10_000)
        assertThat(listing.fetched).containsExactly(5_430)
    }

    @Test
    fun `a listing that shifted is searched for the photo within the page`() = runBlocking<Unit> {
        val listing = Listing(total = 100)
        // The grid said row 23; the photo is at row 25 by now.
        val w = window(listing, start = 23)

        assertThat(w.start("p25")).isTrue()
        assertThat(w.index).isEqualTo(25)
        assertThat(window(Listing(total = 100), start = 23).start("gone")).isFalse()
    }

    @Test
    fun `steps cross pages and wrap at both ends`() = runBlocking<Unit> {
        val listing = Listing(total = 25)
        val w = window(listing, start = 9)
        w.start("p9")

        assertThat(w.step(1)).isEqualTo("p10")
        assertThat(w.step(-1)).isEqualTo("p9")

        val atEnd = window(listing, start = 24).also { it.start("p24") }
        assertThat(atEnd.step(1)).isEqualTo("p0")
        assertThat(atEnd.step(-1)).isEqualTo("p24")
    }

    @Test
    fun `non-photo rows are stepped over`() = runBlocking<Unit> {
        val w = window(Listing(total = 30, others = setOf(4, 5, 6)), start = 3)
        w.start("p3")

        assertThat(w.step(1)).isEqualTo("p7")
        assertThat(w.index).isEqualTo(7)
        assertThat(w.step(-1)).isEqualTo("p3")
    }

    @Test
    fun `at most maxPages pages stay loaded`() = runBlocking<Unit> {
        val listing = Listing(total = 1_000)
        val w = window(listing, start = 0, pageSize = 10, maxPages = 3)
        w.start("p0")

        repeat(55) { w.step(1) }

        assertThat(w.index).isEqualTo(55)
        assertThat(w.loadedPages().size).isAtMost(3)
        assertThat(w.loadedPages()).contains(50)
    }

    private fun located(listing: Listing, itemId: String, locate: suspend () -> Int?) = runBlocking {
        PhotoListingWindow.located(itemId, locate, pageSize = 10) { off, limit -> listing.fetch(off, limit) }
    }

    @Test
    fun `a photo opened without its row opens where the server puts it`() = runBlocking<Unit> {
        val listing = Listing(total = 10_000)
        var asked = 0
        val w = located(listing, "p5432") { asked++; 5_432 }

        assertThat(w).isNotNull()
        assertThat(asked).isEqualTo(1)
        assertThat(w!!.index).isEqualTo(5_432)
        assertThat(w.total).isEqualTo(10_000)
        assertThat(listing.fetched).containsExactly(5_430)
        assertThat(w.step(1)).isEqualTo("p5433")
    }

    @Test
    fun `a 404 or a failed position request opens no window and reads nothing`() {
        val listing = Listing(total = 100)

        // 404: an older server, or the photo isn't in that listing.
        assertThat(located(listing, "p5") { null }).isNull()
        assertThat(located(listing, "p5") { throw java.io.IOException("offline") }).isNull()
        assertThat(listing.fetched).isEmpty()
    }

    @Test
    fun `a position whose page no longer holds the photo opens no window`() {
        assertThat(located(Listing(total = 100), "p5") { 50 }).isNull()
    }

    @Test
    fun `a failed page at the position opens no window`() = runBlocking<Unit> {
        val w = PhotoListingWindow.located("p5", { 5 }, pageSize = 10) { _, _ ->
            throw java.io.IOException("offline")
        }
        assertThat(w).isNull()
    }

    @Test
    fun `a cancelled position request stays cancelled`() {
        val thrown = runCatching {
            located(Listing(total = 100), "p5") { throw kotlinx.coroutines.CancellationException("left") }
        }.exceptionOrNull()
        assertThat(thrown).isInstanceOf(kotlinx.coroutines.CancellationException::class.java)
    }

    @Test
    fun `a failed page load leaves the viewer where it is`() = runBlocking<Unit> {
        var fail = false
        val ok = Listing(total = 20)
        val w = PhotoListingWindow(9, pageSize = 10) { off, limit ->
            if (fail) throw java.io.IOException("offline") else ok.fetch(off, limit)
        }
        w.start("p9")
        fail = true

        assertThat(w.step(1)).isNull()
        assertThat(w.index).isEqualTo(9)
    }
}
