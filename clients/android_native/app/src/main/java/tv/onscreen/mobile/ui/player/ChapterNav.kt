package tv.onscreen.mobile.ui.player

import tv.onscreen.mobile.data.model.Chapter

/**
 * Pure helpers for the chapter picker. Lookup logic + index resolution
 * live here so the binary-search behaviour is JVM-testable without a
 * player or composable.
 */
object ChapterNav {

    /**
     * Index of the chapter covering [positionMs]. Returns -1 when no
     * chapter covers the position (e.g. before the first chapter
     * starts, which only happens on a malformed file). The Composable
     * uses the index for highlight + scroll-to-active.
     *
     * Chapters from the API are in start-time order; binary search on
     * start_ms gives us the right answer in O(log n) — meaningful for
     * audiobooks with hundreds of chapters.
     */
    fun activeIndex(chapters: List<Chapter>, positionMs: Long): Int {
        if (chapters.isEmpty()) return -1
        if (positionMs < chapters.first().start_ms) return -1
        var lo = 0
        var hi = chapters.size - 1
        var best = -1
        while (lo <= hi) {
            val mid = (lo + hi) ushr 1
            if (chapters[mid].start_ms <= positionMs) {
                best = mid
                lo = mid + 1
            } else {
                hi = mid - 1
            }
        }
        return best
    }

    /** Format a chapter start position as `H:MM:SS` (or `MM:SS` under
     *  one hour). Used by the picker row label so the user sees
     *  "0:43:12 — Chapter 12: ..." style entries. */
    fun formatStart(ms: Long): String {
        val total = ms / 1000
        val h = total / 3600
        val m = (total % 3600) / 60
        val s = total % 60
        return if (h > 0) "%d:%02d:%02d".format(h, m, s) else "%d:%02d".format(m, s)
    }

    /** Display label for a chapter row. Falls back to "Chapter N" when
     *  the title is empty (some encoders ship blank chapter names). */
    fun displayTitle(chapter: Chapter, fallbackIndex: Int): String =
        if (chapter.title.isNotBlank()) chapter.title else "Chapter ${fallbackIndex + 1}"
}
