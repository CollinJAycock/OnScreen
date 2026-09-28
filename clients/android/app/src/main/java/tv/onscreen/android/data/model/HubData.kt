package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/**
 * Combined home-page payload from /api/v1/hub. Server-side defaults
 * every list to empty rather than null, so consumers can render rows
 * unconditionally and rely on isEmpty() for "skip this row" logic.
 *
 * [trending] is a rolling watch_events aggregate (everyone-same).
 * Can come back empty for a fresh install with no watch history yet
 * — the consumer handles the "no rows" case the same way as for
 * continue-watching.
 */
@JsonClass(generateAdapter = true)
data class HubData(
    // Legacy combined feed; older server builds only return this one.
    // Newer builds also return the split arrays below — clients should
    // prefer them when present and fall back to filtering this list
    // otherwise.
    val continue_watching: List<HubItem> = emptyList(),
    val continue_watching_tv: List<HubItem>? = null,
    val continue_watching_movies: List<HubItem>? = null,
    val continue_watching_other: List<HubItem>? = null,
    val recently_added: List<HubItem> = emptyList(),
    val recently_added_by_library: List<HubLibraryRow> = emptyList(),
    val trending: List<HubItem> = emptyList(),
    // v2.5 per-user watch rows. Older servers omit both keys; the defaults
    // keep them parsing (and the rows simply don't render).
    //
    // [next_up]: per show the caller is part-way through, the next unwatched
    // episode. Tiles ARE episodes (id/title/type=episode) carrying
    // show_id / show_title / season_number / episode_number.
    val next_up: List<HubItem> = emptyList(),
    // [plan_to_watch]: items the caller set to Plan to Watch, newest first.
    val plan_to_watch: List<HubItem> = emptyList(),
)

@JsonClass(generateAdapter = true)
data class HubItem(
    val id: String,
    val title: String,
    val type: String,
    val year: Int? = null,
    val poster_path: String? = null,
    val fanart_path: String? = null,
    val thumb_path: String? = null,
    val view_offset_ms: Long? = null,
    val duration_ms: Long? = null,
    val updated_at: Long = 0,
    /** Parent show's name. Set on Next Up episode tiles and on episode rows of
     *  the recently-added strips; omitted everywhere else. */
    val show_title: String? = null,
    /** Next Up episode tiles: the show the episode belongs to and its
     *  position within it (the server sends 0 when the scanner recorded no
     *  number). */
    val show_id: String? = null,
    val season_number: Int? = null,
    val episode_number: Int? = null,
)

/** "Recently added to <library>" strip — library info denormalized so
 *  the row can be labeled without an extra lookup. */
@JsonClass(generateAdapter = true)
data class HubLibraryRow(
    val library_id: String,
    val library_name: String,
    val library_type: String,
    val items: List<HubItem> = emptyList(),
)

