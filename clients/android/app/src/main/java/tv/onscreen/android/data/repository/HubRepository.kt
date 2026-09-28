package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.HubData
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class HubRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    suspend fun getHub(): HubData = api.getHub().data

    /** Hide an item from every Continue Watching row (and Next Up) until the
     *  user next records watch activity on it. An episode or season hides its
     *  show's tile. 204 on success; throws HttpException otherwise. */
    suspend fun dismissContinueWatching(itemId: String) {
        api.dismissContinueWatching(itemId)
    }
}
