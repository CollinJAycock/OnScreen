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

    suspend fun getGenres(libraryId: String): List<String> =
        try { api.getLibraryGenres(libraryId).data } catch (_: Exception) { emptyList() }

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
