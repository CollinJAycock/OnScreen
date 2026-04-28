package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/**
 * One TMDB-discover entry returned from /api/v1/discover/search.
 *
 * The search page renders these as a "Request" row alongside the
 * library results — items the user could add to their library if
 * they're not already there. `in_library = true` items are
 * filtered out client-side because the same title would otherwise
 * appear twice (once in the library row, once here).
 *
 * Mirrors web/src/lib/api.ts → DiscoverItem.
 */
@JsonClass(generateAdapter = true)
data class DiscoverItem(
    val type: String, // "movie" | "show"
    val tmdb_id: Int,
    val title: String,
    val year: Int? = null,
    val overview: String? = null,
    val rating: Double? = null,
    val poster_url: String? = null,
    val fanart_url: String? = null,
    val in_library: Boolean = false,
    val library_item_id: String? = null,
    val has_active_request: Boolean = false,
    val active_request_id: String? = null,
    val active_request_status: String? = null,
)
