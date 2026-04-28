package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.CreateRequestBody
import tv.onscreen.android.data.model.DiscoverItem
import tv.onscreen.android.data.model.MediaRequest
import javax.inject.Inject
import javax.inject.Singleton

/**
 * TMDB discover + media-request endpoints. Used by the search
 * screen to surface "items not in your library yet" alongside the
 * library results, with a Request action that mirrors what the
 * web client does.
 *
 * Discover failures are non-fatal at this layer — the search page
 * shows library results regardless. Bubbling exceptions up so the
 * caller can decide whether to log them (e.g., "TMDB key not
 * configured" → silent; rate-limited or 5xx → surface).
 */
@Singleton
open class DiscoverRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    open suspend fun search(query: String, limit: Int = 12): List<DiscoverItem> =
        api.discoverSearch(query, limit).data

    open suspend fun createRequest(type: String, tmdbId: Int): MediaRequest =
        api.createRequest(CreateRequestBody(type = type, tmdb_id = tmdbId)).data
}
