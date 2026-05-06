package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.Chapter

class ChapterNavTest {

    private fun chapter(title: String, startSec: Long, endSec: Long) =
        Chapter(title = title, start_ms = startSec * 1000, end_ms = endSec * 1000)

    private val sample = listOf(
        chapter("Prologue", 0, 120),
        chapter("Chapter 1", 120, 600),
        chapter("Chapter 2", 600, 1200),
        chapter("Epilogue", 1200, 1500),
    )

    @Test
    fun `activeIndex returns -1 for empty input`() {
        assertThat(ChapterNav.activeIndex(emptyList(), 0)).isEqualTo(-1)
    }

    @Test
    fun `activeIndex returns -1 before the first chapter starts`() {
        // Audiobooks usually start at 0 ms but a malformed file might
        // have chapter[0].start_ms > 0 — handle gracefully.
        val late = listOf(chapter("Late", 5000, 6000))
        assertThat(ChapterNav.activeIndex(late, 0)).isEqualTo(-1)
        assertThat(ChapterNav.activeIndex(late, 4_999_999)).isEqualTo(-1)
    }

    @Test
    fun `activeIndex returns the chapter covering the position`() {
        assertThat(ChapterNav.activeIndex(sample, 0)).isEqualTo(0)
        assertThat(ChapterNav.activeIndex(sample, 60_000)).isEqualTo(0)
        assertThat(ChapterNav.activeIndex(sample, 120_000)).isEqualTo(1)
        assertThat(ChapterNav.activeIndex(sample, 599_999)).isEqualTo(1)
        assertThat(ChapterNav.activeIndex(sample, 600_000)).isEqualTo(2)
        assertThat(ChapterNav.activeIndex(sample, 1_200_000)).isEqualTo(3)
        assertThat(ChapterNav.activeIndex(sample, 1_499_999)).isEqualTo(3)
    }

    @Test
    fun `activeIndex past the last chapter still returns the last index`() {
        // Audiobooks: a position past the last chapter end means the
        // user has finished. Chapter list still reads "Epilogue" as
        // the active highlight rather than disappearing — better UX.
        assertThat(ChapterNav.activeIndex(sample, 9_999_999)).isEqualTo(3)
    }

    @Test
    fun `formatStart formats under one hour as M_SS`() {
        assertThat(ChapterNav.formatStart(0)).isEqualTo("0:00")
        assertThat(ChapterNav.formatStart(45_000)).isEqualTo("0:45")
        assertThat(ChapterNav.formatStart(120_000)).isEqualTo("2:00")
        assertThat(ChapterNav.formatStart(59 * 60_000L + 59_000)).isEqualTo("59:59")
    }

    @Test
    fun `formatStart formats one-hour-plus as H_MM_SS`() {
        assertThat(ChapterNav.formatStart(3_600_000)).isEqualTo("1:00:00")
        assertThat(ChapterNav.formatStart(3_723_000)).isEqualTo("1:02:03")
        assertThat(ChapterNav.formatStart(15L * 3_600_000)).isEqualTo("15:00:00")
    }

    @Test
    fun `displayTitle prefers title and falls back to Chapter N`() {
        // Some encoders emit Chapter N records with blank titles. UI
        // would show a confusing empty row; the helper substitutes
        // the index so the user always sees something readable.
        val withTitle = chapter("Chapter 7: The Reveal", 0, 100)
        val blank = chapter("", 0, 100)
        val whitespaceOnly = chapter("   ", 0, 100)
        assertThat(ChapterNav.displayTitle(withTitle, 6)).isEqualTo("Chapter 7: The Reveal")
        assertThat(ChapterNav.displayTitle(blank, 6)).isEqualTo("Chapter 7")
        assertThat(ChapterNav.displayTitle(whitespaceOnly, 0)).isEqualTo("Chapter 1")
    }
}
