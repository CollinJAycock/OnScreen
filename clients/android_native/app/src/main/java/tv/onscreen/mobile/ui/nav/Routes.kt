package tv.onscreen.mobile.ui.nav

object Routes {
    const val PAIR = "pair"
    const val HUB = "hub"
    const val LIBRARY = "library/{id}"
    const val ITEM = "item/{id}"
    const val SEARCH = "search"
    const val PLAYER = "player/{id}"
    /** Book / comic reader. CBZ/CBR pages render via Coil; EPUB renders
     *  in a WebView with bundled epub.js. */
    const val BOOK = "book/{id}"
    const val FAVORITES = "favorites"
    const val HISTORY = "history"
    const val COLLECTIONS = "collections"
    const val COLLECTION = "collection/{id}"
    const val DOWNLOADS = "downloads"
    /** User-owned playlists (static + smart). v2.2 phone parity. */
    const val PLAYLISTS = "playlists"
    /** Photo extras — timeline + geotagged tabs for a single photo
     *  library. Reachable from the LibraryScreen action bar when the
     *  library is type=photo. */
    const val PHOTO_EXTRAS = "photo-extras/{libraryId}"
    const val PHOTO = "photo/{id}"
    const val AUTHOR = "author/{id}"
    const val SERIES = "series/{id}"
    const val SETTINGS = "settings"
    const val ABOUT = "about"

    fun library(id: String) = "library/$id"
    fun item(id: String) = "item/$id"
    fun player(id: String) = "player/$id"
    fun book(id: String) = "book/$id"
    fun collection(id: String) = "collection/$id"
    fun photo(id: String) = "photo/$id"
    fun author(id: String) = "author/$id"
    fun series(id: String) = "series/$id"
    fun photoExtras(libraryId: String) = "photo-extras/$libraryId"
}
