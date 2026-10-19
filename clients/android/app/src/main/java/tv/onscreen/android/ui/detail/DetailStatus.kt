package tv.onscreen.android.ui.detail

import tv.onscreen.android.R
import tv.onscreen.android.ui.watchlist.WatchlistUi

/**
 * Pure helpers behind the details page's Status action (the caller's
 * watch-status: Plan to Watch / Watching / On Hold / Completed / Dropped).
 * No Android types beyond resource ids — unit-tested in DetailStatusTest.
 * Labels come from the Watchlist screen's, so the two read the same.
 */
object DetailStatus {

    /** The types the details page offers a Status on. (The server takes a
     *  few more — tracks, photos — but those have no details page of their
     *  own worth tracking from.) */
    private val STATUSABLE = setOf(
        "movie", "show", "season", "episode", "album", "artist", "audiobook", "book",
    )

    fun isStatusable(type: String): Boolean = type in STATUSABLE

    /**
     * The button's label for [status] (null = none set): "Status: Plan to
     * Watch" as (R.string.status_label, the status's label), or "Add to
     * Watchlist" as (R.string.status_add, null). An unknown status reads as
     * none.
     */
    fun buttonLabel(status: String?): Pair<Int, Int?> {
        val res = status?.let { WatchlistUi.labelRes(it) }
        return if (res == null) R.string.status_add to null else R.string.status_label to res
    }

    /** One picker row: a status to set, or Remove ([status] null). */
    data class Choice(val status: String?, val labelRes: Int)

    /** The picker's rows: the five statuses in Watchlist row order, then
     *  "Remove from Watchlist" when one is set. */
    fun choices(current: String?): List<Choice> {
        val statuses = WatchlistUi.STATUSES.map { Choice(it, WatchlistUi.labelRes(it)!!) }
        return if (current in WatchlistUi.STATUSES) statuses + Choice(null, R.string.watchlist_remove) else statuses
    }

    /** The row to show checked: [current]'s, or -1 (none checked). */
    fun checkedIndex(current: String?): Int = WatchlistUi.STATUSES.indexOf(current)
}
