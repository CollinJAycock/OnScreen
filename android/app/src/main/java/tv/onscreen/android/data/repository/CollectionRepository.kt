package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.CollectionItem
import tv.onscreen.android.data.model.MediaCollection
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class CollectionRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    suspend fun getCollections(): List<MediaCollection> = api.getCollections().data

    suspend fun getItems(
        collectionId: String,
        limit: Int = 50,
        offset: Int = 0,
    ): Pair<List<CollectionItem>, Int> {
        val resp = api.getCollectionItems(collectionId, limit, offset)
        return resp.data to resp.meta.total
    }
}
