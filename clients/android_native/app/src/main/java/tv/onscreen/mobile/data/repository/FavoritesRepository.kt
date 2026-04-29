package tv.onscreen.mobile.data.repository

import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.FavoriteItem
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
open class FavoritesRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    open suspend fun list(limit: Int = 50, offset: Int = 0): List<FavoriteItem> =
        api.getFavorites(limit, offset).data

    open suspend fun add(itemId: String) {
        api.addFavorite(itemId)
    }

    open suspend fun remove(itemId: String) {
        api.removeFavorite(itemId)
    }
}
