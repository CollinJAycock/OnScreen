package tv.onscreen.android.ui.watchlist

import tv.onscreen.android.R
import tv.onscreen.android.data.model.WatchStatusValues
import tv.onscreen.android.data.model.WatchlistItem
import tv.onscreen.android.ui.common.WatchStateUi

/**
 * Pure helpers behind the Watchlist screen: row order, card labels and where
 * focus lands after the rows are rebuilt. No Android types beyond resource
 * ids — unit-tested in WatchlistUiTest. Mirrors web/src/lib/watchlist.ts so
 * the two clients label things the same.
 */
object WatchlistUi {

    /** Row order: Plan to Watch first, then the tracker progression. (Not
     *  [WatchStatusValues.all], which is the server's order.) */
    val STATUSES: List<String> = listOf(
        WatchStatusValues.PlanToWatch,
        WatchStatusValues.Watching,
        WatchStatusValues.OnHold,
        WatchStatusValues.Completed,
        WatchStatusValues.Dropped,
    )

    /** The row label for [status], or null for an unknown one. */
    fun labelRes(status: String): Int? = when (status) {
        WatchStatusValues.PlanToWatch -> R.string.watchlist_plan_to_watch
        WatchStatusValues.Watching -> R.string.watchlist_watching
        WatchStatusValues.OnHold -> R.string.watchlist_on_hold
        WatchStatusValues.Completed -> R.string.watchlist_completed
        WatchStatusValues.Dropped -> R.string.watchlist_dropped
        else -> null
    }

    /**
     * The card's (title, subtitle) when it gets a second line, else null (the
     * title alone, on two lines — like every other poster card):
     *  - episode: "Episode title" / "Show · S2 · E5"
     *  - season:  "Season 2" / "Show"
     *  - track:   "Track" / "Artist · Album"
     *  - album, audiobook, book: "Album" / "Artist · 2020"
     */
    fun cardTitles(item: WatchlistItem): Pair<String, String>? =
        cardSubtitle(item).takeIf { it.isNotEmpty() }?.let { item.title to it }

    fun cardSubtitle(item: WatchlistItem): String = when (item.type) {
        "episode" -> listOf(
            item.grandparent_title?.takeIf { it.isNotBlank() },
            WatchStateUi.episodeCode(item.parent_index, item.index).takeIf { it.isNotEmpty() },
        ).filterNotNull().joinToString(" · ")
        "season" -> item.parent_title.orEmpty()
        "track" -> listOf(item.grandparent_title, item.parent_title)
            .filter { !it.isNullOrBlank() }.joinToString(" · ")
        "album", "audiobook", "book" -> listOf(item.parent_title, item.year?.toString())
            .filter { !it.isNullOrBlank() }.joinToString(" · ")
        else -> ""
    }

    /** The statuses a status-change menu offers for an item in [current]:
     *  every other status, in row order. */
    fun moveTargets(current: String): List<String> = STATUSES.filter { it != current }

    /**
     * Where focus goes after the rows are rebuilt (a status changed on the
     * detail page, a row emptied, a card moved), as (row, index-in-row), or
     * null when there are no rows.
     *
     * The card the user was on ([itemId] in [status]'s row) when it is still
     * there; else the same position in that row (its neighbour slid in); else,
     * when the row itself went away, the row now at its old place, from its
     * first card.
     */
    fun focusTarget(
        rows: List<Pair<String, List<String>>>,
        status: String?,
        itemId: String?,
        rowIndex: Int,
        itemIndex: Int,
    ): Pair<Int, Int>? {
        if (rows.isEmpty()) return null
        val r = rows.indexOfFirst { it.first == status }
        if (r < 0) return rowIndex.coerceIn(0, rows.size - 1) to 0
        val ids = rows[r].second
        val found = if (itemId != null) ids.indexOf(itemId) else -1
        if (found >= 0) return r to found
        return r to itemIndex.coerceIn(0, (ids.size - 1).coerceAtLeast(0))
    }
}
