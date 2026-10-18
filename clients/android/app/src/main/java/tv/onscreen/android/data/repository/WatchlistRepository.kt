package tv.onscreen.android.data.repository

import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.WatchStatusUpdate
import tv.onscreen.android.data.model.WatchlistCounts
import tv.onscreen.android.data.model.WatchlistItem
import javax.inject.Inject
import javax.inject.Singleton

/** The caller's watch-status list (Watchlist screen) and the status writes
 *  its card menu makes. */
@Singleton
open class WatchlistRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    /** One page of [status]'s list, plus that list's total. */
    open suspend fun page(status: String, limit: Int = PAGE_SIZE, offset: Int = 0): Pair<List<WatchlistItem>, Int> {
        val resp = api.getWatchlist(status, limit, offset)
        return resp.data to resp.meta.total
    }

    open suspend fun counts(): WatchlistCounts = api.getWatchlistCounts().data

    /** Move [itemId] to [status] (PUT /items/{id}/watch-status). */
    open suspend fun setStatus(itemId: String, status: String) {
        api.setWatchStatus(itemId, WatchStatusUpdate(status)).orThrow()
    }

    /** Take [itemId] off the watchlist (DELETE /items/{id}/watch-status). */
    open suspend fun clearStatus(itemId: String) {
        api.clearWatchStatus(itemId).orThrow()
    }

    // These two endpoints are declared Response<Unit>, so Retrofit does not
    // throw on a non-2xx by itself.
    private fun Response<Unit>.orThrow() {
        if (!isSuccessful) throw HttpException(this)
    }

    companion object {
        /** Cards fetched per page as a row scrolls. */
        const val PAGE_SIZE = 50

        /** The server's per-request cap on `limit`. */
        const val MAX_LIMIT = 200
    }
}
