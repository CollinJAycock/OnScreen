package tv.onscreen.mobile.data.repository

import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.WatchStatus
import tv.onscreen.mobile.data.model.WatchlistCounts
import tv.onscreen.mobile.data.model.WatchlistItem
import tv.onscreen.mobile.data.model.WatchlistSort
import javax.inject.Inject
import javax.inject.Singleton

/** The watchlist listing (v2.6). Changing / removing a status goes through
 *  [ItemRepository.setWatchStatus] / [ItemRepository.clearWatchStatus] —
 *  the same per-item route the detail page's picker uses. */
@Singleton
open class WatchlistRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    /** One page of [status] items, plus the server's total for the query. */
    open suspend fun list(
        status: WatchStatus,
        type: String? = null,
        sort: WatchlistSort = WatchlistSort.RECENT,
        limit: Int = 50,
        offset: Int = 0,
    ): Pair<List<WatchlistItem>, Int> {
        val res = api.getWatchlist(status.wire, type, sort.wire, limit, offset)
        return res.data to res.meta.total
    }

    open suspend fun counts(): WatchlistCounts = api.getWatchlistCounts().data
}
