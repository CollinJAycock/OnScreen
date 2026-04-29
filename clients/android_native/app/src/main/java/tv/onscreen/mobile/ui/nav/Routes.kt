package tv.onscreen.mobile.ui.nav

object Routes {
    const val PAIR = "pair"
    const val HUB = "hub"
    const val LIBRARY = "library/{id}"
    const val ITEM = "item/{id}"
    const val SEARCH = "search"
    const val PLAYER = "player/{id}"
    const val FAVORITES = "favorites"
    const val HISTORY = "history"
    const val COLLECTIONS = "collections"
    const val COLLECTION = "collection/{id}"
    const val DOWNLOADS = "downloads"
    const val PHOTO = "photo/{id}"

    fun library(id: String) = "library/$id"
    fun item(id: String) = "item/$id"
    fun player(id: String) = "player/$id"
    fun collection(id: String) = "collection/$id"
    fun photo(id: String) = "photo/$id"
}
