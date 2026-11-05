package tv.onscreen.mobile.data.repository

import retrofit2.HttpException
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.Library
import tv.onscreen.mobile.data.model.LibraryLetter
import tv.onscreen.mobile.data.model.MediaItem
import tv.onscreen.mobile.data.model.RandomLibraryItem
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class LibraryRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    suspend fun getLibraries(): List<Library> = api.getLibraries().data

    suspend fun getGenres(libraryId: String): List<String> =
        try { api.getLibraryGenres(libraryId).data } catch (_: Exception) { emptyList() }

    suspend fun getItems(
        libraryId: String,
        limit: Int = 50,
        offset: Int = 0,
        sort: String? = null,
        sortDir: String? = null,
        genre: String? = null,
        /** `?watch=` bucket (unwatched / in_progress / watched); null = all. */
        watch: String? = null,
    ): Pair<List<MediaItem>, Int> {
        val resp = api.getLibraryItems(libraryId, limit, offset, sort, sortDir, genre, watch)
        return resp.data to resp.meta.total
    }

    /** The listing's A-Z jump index for the same parameters as [getItems].
     *  Empty when the listing isn't sorted by title, and on a server without
     *  the endpoint (404, pre-v2.7) — the screen then shows no letter rail.
     *  Other failures throw. */
    suspend fun getLetters(
        libraryId: String,
        sort: String? = null,
        sortDir: String? = null,
        genre: String? = null,
        watch: String? = null,
    ): List<LibraryLetter> = try {
        api.getLibraryItemLetters(libraryId, sort, sortDir, genre, watch).data
    } catch (e: HttpException) {
        if (e.code() == 404) emptyList() else throw e
    }

    /** "Surprise me": a random visible item under the same filters as the
     *  listing, or null when nothing matches (the server's 404). Other
     *  failures — incl. 501 from a server without the watch store — throw. */
    suspend fun randomItem(
        libraryId: String,
        genre: String? = null,
        watch: String? = null,
    ): RandomLibraryItem? = try {
        api.getRandomLibraryItem(libraryId, genre, watch).data
    } catch (e: HttpException) {
        if (e.code() == 404) null else throw e
    }
}
