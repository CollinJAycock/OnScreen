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
    /** TMDB discover + in-app request submit. New in v2.2 phone parity. */
    const val DISCOVER = "discover"
    /** User-owned playlists (static + smart). v2.2 phone parity. */
    const val PLAYLISTS = "playlists"
    /** Photo extras — timeline + geotagged tabs for a single photo
     *  library. Reachable from the LibraryScreen action bar when the
     *  library is type=photo. */
    const val PHOTO_EXTRAS = "photo-extras/{libraryId}"
    const val PHOTO = "photo/{id}"
    const val AUTHOR = "author/{id}"
    const val SERIES = "series/{id}"

    fun library(id: String) = "library/$id"
    fun item(id: String) = "item/$id"
    fun player(id: String) = "player/$id"
    fun collection(id: String) = "collection/$id"
    fun photo(id: String) = "photo/$id"
    fun author(id: String) = "author/$id"
    fun series(id: String) = "series/$id"
    fun photoExtras(libraryId: String) = "photo-extras/$libraryId"
}
