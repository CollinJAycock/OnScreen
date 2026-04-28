package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/**
 * Combined home-page payload from /api/v1/hub. Server-side defaults
 * every list to empty rather than null, so consumers can render rows
 * unconditionally and rely on isEmpty() for "skip this row" logic.
 *
 * [trending] and [because_you_watched] are v2.1 discovery additions:
 * trending is a rolling watch_events aggregate (everyone-same), and
 * because-you-watched is item-to-item watch-cooccurrence keyed by
 * recently-completed seeds (per-user). Both can come back empty for
 * a fresh install with no watch history yet — the consumer handles
 * the "no rows" case the same way as for continue-watching.
 */
@JsonClass(generateAdapter = true)
data class HubData(
    val continue_watching: List<HubItem> = emptyList(),
    val recently_added: List<HubItem> = emptyList(),
    val recently_added_by_library: List<HubLibraryRow> = emptyList(),
    val trending: List<HubItem> = emptyList(),
    val because_you_watched: List<BecauseYouWatched> = emptyList(),
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

/** One personalised recommendations row — labelled by the [seed] item
 *  the user recently watched, populated with cooccurrent [items]. */
@JsonClass(generateAdapter = true)
data class BecauseYouWatched(
    val seed: HubSeedItem,
    val items: List<HubItem> = emptyList(),
)

@JsonClass(generateAdapter = true)
data class HubSeedItem(
    val id: String,
    val title: String,
    val poster_path: String? = null,
    val thumb_path: String? = null,
    val updated_at: Long = 0,
)
