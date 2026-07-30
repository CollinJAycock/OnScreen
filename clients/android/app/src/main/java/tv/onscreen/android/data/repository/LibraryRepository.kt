package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.Library
import tv.onscreen.android.data.model.MediaItem
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class LibraryRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    suspend fun getLibraries(): List<Library> = api.getLibraries().data

    /** Genre names for the browse filter, most-populated order preserved from
     *  the server. Returns empty on failure — a missing filter is a degraded
     *  browse screen, not a reason to fail the whole library load. */
    suspend fun getGenres(libraryId: String): List<String> =
        try {
            api.getLibraryGenres(libraryId).data.map { it.name }
        } catch (e: Exception) {
            // Logged, not silent. This swallow previously hid a permanent
            // decode failure — the endpoint returns objects and the client
            // asked for strings — so the filter was empty forever with nothing
            // to show for it.
            android.util.Log.w("LibraryRepository", "genres fetch failed for $libraryId", e)
            emptyList()
        }

    suspend fun getItems(
        libraryId: String,
        limit: Int = 50,
        offset: Int = 0,
        sort: String? = null,
        sortDir: String? = null,
        genre: String? = null,
    ): Pair<List<MediaItem>, Int> {
        val resp = api.getLibraryItems(libraryId, limit, offset, sort, sortDir, genre)
        return resp.data to resp.meta.total
    }
}
