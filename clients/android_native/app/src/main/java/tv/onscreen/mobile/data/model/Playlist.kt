package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

/**
 * A user-owned playlist. Two flavours, both stored in the server's
 * `collections` table:
 *   - `playlist` — static, manually-curated list of items
 *   - `smart_playlist` — rule-evaluated; items resolve at query time
 *
 * The [rules] field is null for static playlists and populated for
 * smart playlists. UI branches on [type] to show a "Smart" badge and
 * gate manual add/remove.
 */
@JsonClass(generateAdapter = true)
data class Playlist(
    val id: String,
    val name: String,
    val description: String? = null,
    val type: String, // "playlist" | "smart_playlist"
    val rules: SmartPlaylistRules? = null,
    val created_at: String,
    val updated_at: String,
)

/**
 * Filters the smart-playlist evaluator runs. Mirrors the v2.1 server
 * grammar in `internal/api/v1/playlists.go` — keep field names lined up
 * with the wire JSON. Empty / null fields = no constraint on that
 * dimension.
 *
 *   - [types] is OR-within (any of the listed types qualifies)
 *   - [genres] is OR-within
 *   - [yearMin] / [yearMax] are inclusive range bounds
 *   - [ratingMin] is the minimum TMDB rating (0-10 scale)
 *   - [limit] caps result count; server defaults to 50, max 500
 */
@JsonClass(generateAdapter = true)
data class SmartPlaylistRules(
    val types: List<String> = emptyList(),
    val genres: List<String> = emptyList(),
    val year_min: Int? = null,
    val year_max: Int? = null,
    val rating_min: Double? = null,
    val limit: Int? = null,
)

@JsonClass(generateAdapter = true)
data class CreatePlaylistRequest(
    val name: String,
    val description: String? = null,
    /** Non-null body promotes the new row to a smart playlist. */
    val rules: SmartPlaylistRules? = null,
)
