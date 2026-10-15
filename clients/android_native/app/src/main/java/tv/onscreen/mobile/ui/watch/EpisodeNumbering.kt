package tv.onscreen.mobile.ui.watch

/*
 * Multi-episode files (v2.6). An "S01E01-E02" file is ONE item: index 1,
 * index_end 2 — the server never lists the covered episode 2 on its own. The
 * list rows show it as a range, and everything that walks to "the next
 * episode" starts after the LAST episode the file covers.
 */

/** "1", or "1–2" for a file covering episodes 1 to 2. A range that doesn't
 *  run forward (index_end missing, equal or smaller) is the single number. */
fun episodeNumberLabel(index: Int, indexEnd: Int?): String =
    if (indexEnd != null && indexEnd > index) "$index–$indexEnd" else index.toString()

/** The last episode number [index] / [indexEnd] covers: what "the next
 *  episode" must come after. */
fun lastEpisodeIndex(index: Int, indexEnd: Int?): Int =
    if (indexEnd != null && indexEnd > index) indexEnd else index
