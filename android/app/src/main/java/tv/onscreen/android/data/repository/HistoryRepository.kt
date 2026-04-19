package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.HistoryItem
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
open class HistoryRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    open suspend fun list(limit: Int = 50, offset: Int = 0): List<HistoryItem> =
        api.getHistory(limit, offset).data
}
