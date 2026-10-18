package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

/**
 * One card on the watchlist (`GET /api/v1/watchlist`, v2.6): an item the
 * caller gave a watching status, with enough ancestry to label an episode
 * ("Show · S2 E5") or a track. Mirrors `WatchlistItem` in
 * internal/api/v1/watchlist.go. `poster_path` already falls back to the
 * parent's / grandparent's poster server-side, so an episode card shows the
 * show's art. Optional keys are omitted by the server when empty.
 */
@JsonClass(generateAdapter = true)
data class WatchlistItem(
    val id: String,
    val library_id: String = "",
    val type: String,
    val title: String,
    val year: Int? = null,
    /** Episode / season / track number when the type has one. */
    val index: Int? = null,
    val parent_id: String? = null,
    /** The season for an episode, the album for a track, the show for a season. */
    val parent_title: String? = null,
    val parent_index: Int? = null,
    val grandparent_id: String? = null,
    /** The show for an episode, the artist for a track. */
    val grandparent_title: String? = null,
    val poster_path: String? = null,
    val fanart_path: String? = null,
    val thumb_path: String? = null,
    val duration_ms: Long? = null,
    /** watched / in_progress / unwatched ([WatchStateValue]). */
    val watch_state: String? = null,
    /** The status wire value ([WatchStatus.wire]). */
    val status: String = "",
    val status_created_at: String? = null,
    val status_updated_at: String? = null,
)

/** One status tab's badge: its total and the per-type breakdown. */
@JsonClass(generateAdapter = true)
data class WatchlistStatusCount(
    val total: Int = 0,
    val types: Map<String, Int> = emptyMap(),
)

/**
 * `GET /api/v1/watchlist/counts` payload (inside the `data` envelope).
 * Keyed by status wire value; a status with nothing in it may be missing.
 */
@JsonClass(generateAdapter = true)
data class WatchlistCounts(
    val total: Int = 0,
    val statuses: Map<String, WatchlistStatusCount> = emptyMap(),
)

/** `sort=` for the watchlist listing. */
enum class WatchlistSort(val wire: String, val label: String) {
    RECENT("recent", "Recently changed"),
    TITLE("title", "Title"),
}
