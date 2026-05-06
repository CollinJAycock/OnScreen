package tv.onscreen.mobile.ui.photo

import tv.onscreen.mobile.data.model.PhotoTimelineBucket

/** Helpers for the PhotoExtras (timeline / map) screen. Pure module
 *  — no Compose / Android imports. Cover the tricky bits:
 *   - turning a numeric month (1-12) into a label without LocaleData
 *   - grouping the per-month timeline buckets by year for a sticky
 *     section header layout
 */
object PhotoExtras {

    private val MONTH_NAMES = listOf(
        "January", "February", "March", "April", "May", "June",
        "July", "August", "September", "October", "November", "December",
    )

    /** Display label for a month (1-12). Returns "Unknown" for
     *  out-of-range values rather than throwing — defensive against
     *  a server emitting month=0 from a malformed EXIF. */
    fun monthName(month: Int): String =
        if (month in 1..12) MONTH_NAMES[month - 1] else "Unknown"

    /** Group buckets by year, preserving server order within each
     *  year. Returns ordered pairs of (year, bucketsForYear) so the
     *  UI can render with a sticky header per year. */
    fun groupByYear(buckets: List<PhotoTimelineBucket>): List<Pair<Int, List<PhotoTimelineBucket>>> {
        if (buckets.isEmpty()) return emptyList()
        val out = mutableListOf<Pair<Int, MutableList<PhotoTimelineBucket>>>()
        for (b in buckets) {
            val last = out.lastOrNull()
            if (last != null && last.first == b.year) last.second.add(b)
            else out.add(b.year to mutableListOf(b))
        }
        return out.map { (year, items) -> year to items.toList() }
    }

    /** Pluralise the count label for a bucket / total: "1 photo",
     *  "12 photos". Common UI need across the timeline + map. */
    fun photosLabel(n: Int): String = if (n == 1) "1 photo" else "$n photos"
}
