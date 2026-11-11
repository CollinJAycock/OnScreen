package tv.onscreen.mobile.ui.photo

import androidx.annotation.StringRes
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.model.PhotoTimelineBucket
import tv.onscreen.mobile.i18n.UiText

/** Helpers for the PhotoExtras (timeline / map) screen. Pure module
 *  — no Compose / Android imports. Cover the tricky bits:
 *   - turning a numeric month (1-12) into a label without LocaleData
 *   - grouping the per-month timeline buckets by year for a sticky
 *     section header layout
 */
object PhotoExtras {

    private val MONTH_NAMES = listOf(
        R.string.photo_month_1, R.string.photo_month_2, R.string.photo_month_3,
        R.string.photo_month_4, R.string.photo_month_5, R.string.photo_month_6,
        R.string.photo_month_7, R.string.photo_month_8, R.string.photo_month_9,
        R.string.photo_month_10, R.string.photo_month_11, R.string.photo_month_12,
    )

    /** Display label for a month (1-12), as a string resource. "Unknown"
     *  for out-of-range values rather than throwing — defensive against
     *  a server emitting month=0 from a malformed EXIF. */
    @StringRes
    fun monthName(month: Int): Int =
        if (month in 1..12) MONTH_NAMES[month - 1] else R.string.photo_month_unknown

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

    /** The count label for a bucket / total: "1 photo", "12 photos".
     *  Common UI need across the timeline + map. */
    fun photosLabel(n: Int): UiText = UiText.plural(R.plurals.photo_count, n)

}
