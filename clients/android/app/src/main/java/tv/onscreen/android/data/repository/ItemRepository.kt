package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.model.Marker
import tv.onscreen.android.data.model.ProgressRequest
import tv.onscreen.android.data.model.SearchResult
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
open class ItemRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    open suspend fun getItem(id: String): ItemDetail = api.getItem(id).data

    open suspend fun getChildren(id: String): List<ChildItem> =
        api.getChildren(id).data

    open suspend fun updateProgress(
        itemId: String,
        offsetMs: Long,
        durationMs: Long,
        state: String,
    ) {
        api.updateProgress(itemId, ProgressRequest(offsetMs, durationMs, state))
    }

    /** Intro / credits markers for an episode. Movies and containers
     *  return an empty list; the network failure path also returns
     *  empty so the player doesn't fail-loud over an optional feature. */
    open suspend fun getMarkers(itemId: String): List<Marker> =
        try { api.getMarkers(itemId).data } catch (_: Exception) { emptyList() }

    open suspend fun search(query: String, limit: Int = 30, libraryId: String? = null): List<SearchResult> =
        api.search(query, limit, libraryId).data
}
