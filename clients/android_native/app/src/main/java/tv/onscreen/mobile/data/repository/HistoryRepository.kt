package tv.onscreen.mobile.data.repository

import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.HistoryItem
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
open class HistoryRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    open suspend fun list(limit: Int = 50, offset: Int = 0): List<HistoryItem> =
        api.getHistory(limit, offset).data
}
