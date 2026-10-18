package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/**
 * One card on the watchlist (v2.6, GET /api/v1/watchlist): the item, enough
 * ancestry to label an episode ("Show · S2 · E5") or a track, the caller's
 * watch-status and the rolled-up watch state. Mirrors WatchlistItem in
 * internal/api/v1/watchlist.go.
 *
 * [index] is the episode / season / track number; [parent_title] /
 * [parent_index] the season for an episode, the album for a track, the show
 * for a season; [grandparent_title] the show for an episode, the artist for a
 * track. [poster_path] already falls back to the parent's / grandparent's
 * poster server-side, so an episode card shows the show's art.
 */
@JsonClass(generateAdapter = true)
data class WatchlistItem(
    val id: String,
    val library_id: String = "",
    val type: String,
    val title: String,
    val year: Int? = null,
    val index: Int? = null,
    val parent_id: String? = null,
    val parent_title: String? = null,
    val parent_index: Int? = null,
    val grandparent_id: String? = null,
    val grandparent_title: String? = null,
    val poster_path: String? = null,
    val fanart_path: String? = null,
    val thumb_path: String? = null,
    val duration_ms: Long? = null,
    /** watched / in_progress / unwatched (a show rolls up its episodes). */
    val watch_state: String? = null,
    /** One of [WatchStatusValues]. */
    val status: String = "",
    /** RFC 3339: when the item first got any status. */
    val status_created_at: String? = null,
    /** RFC 3339: when the item entered its current status. */
    val status_updated_at: String? = null,
)

/**
 * GET /api/v1/watchlist/counts: per-status totals with a per-type breakdown,
 * under the same visibility filters as the listing. Current servers send all
 * five statuses (zero when empty); a missing key reads as zero.
 */
@JsonClass(generateAdapter = true)
data class WatchlistCounts(
    val total: Int = 0,
    val statuses: Map<String, WatchlistStatusCount> = emptyMap(),
) {
    fun totalFor(status: String): Int = statuses[status]?.total ?: 0
}

@JsonClass(generateAdapter = true)
data class WatchlistStatusCount(
    val total: Int = 0,
    val types: Map<String, Int> = emptyMap(),
)
