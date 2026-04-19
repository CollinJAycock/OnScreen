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
}
