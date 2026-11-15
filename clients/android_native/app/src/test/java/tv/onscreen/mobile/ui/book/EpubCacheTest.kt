package tv.onscreen.mobile.ui.book

import com.google.common.truth.Truth.assertThat
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

/** The EPUB cache used to keep every book ever opened; it is now trimmed to a
 *  size cap, least recently opened first. */
class EpubCacheTest {

    @get:Rule val tmp = TemporaryFolder()

    private fun book(name: String, bytes: Int, mtime: Long): File =
        File(tmp.root, name).apply {
            writeBytes(ByteArray(bytes))
            setLastModified(mtime)
        }

    @Test
    fun `oldest books go until the rest fit`() {
        val old = book("a.epub", 40, 1_000_000)
        val mid = book("b.epub", 40, 2_000_000)
        val new = book("c.epub", 40, 3_000_000)

        EpubCache.trim(tmp.root, keep = new, maxBytes = 90)

        assertThat(old.exists()).isFalse()
        assertThat(mid.exists()).isTrue()
        assertThat(new.exists()).isTrue()
    }

    @Test
    fun `the book being opened stays even over the cap, and fetches in progress are left alone`() {
        val opening = book("a.epub", 100, 1_000_000)
        val other = book("b.epub", 10, 2_000_000)
        val inFlight = book("c.epub.tmp", 50, 500_000)

        EpubCache.trim(tmp.root, keep = opening, maxBytes = 50)

        assertThat(opening.exists()).isTrue()
        assertThat(other.exists()).isFalse()
        assertThat(inFlight.exists()).isTrue()
    }

    @Test
    fun `under the cap nothing is deleted`() {
        val a = book("a.epub", 10, 1_000_000)
        val b = book("b.epub", 10, 2_000_000)

        EpubCache.trim(tmp.root, keep = b, maxBytes = 100)

        assertThat(a.exists()).isTrue()
        assertThat(b.exists()).isTrue()
    }
}
