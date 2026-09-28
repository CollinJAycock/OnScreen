package tv.onscreen.mobile.data.repository

import retrofit2.HttpException
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.HubData
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class HubRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    suspend fun getHub(): HubData = api.getHub().data

    /** Remove an item (a movie, or a show's tile) from Continue Watching.
     *  The server answers 404 when there was nothing left to hide — the
     *  tile is already gone, which is what the caller wanted, so that's
     *  success here. Any other failure throws. */
    suspend fun dismissContinueWatching(itemId: String) {
        try {
            api.dismissContinueWatching(itemId)
        } catch (e: HttpException) {
            if (e.code() != 404) throw e
        }
    }
}
