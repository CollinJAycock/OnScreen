package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

// Music discovery (server v2.6, internal/api/v1/music.go). All three routes
// answer the list envelope; a server without them answers 404.

/** GET /music/artists/{id}/similar: an artist like the one asked about, from
 *  the same library, most similar first. */
@JsonClass(generateAdapter = true)
data class SimilarArtist(
    val id: String,
    val title: String = "",
    val poster_path: String? = null,
    /** Similarity in [0, 1]. */
    val score: Double = 0.0,
    val shared_genres: List<String> = emptyList(),
    val co_listened: Boolean = false,
)

/** GET /music/artists/{id}/appears-on: one track on another artist's album
 *  that credits this artist ("featured", or "artist" on a compilation),
 *  newest album first. */
@JsonClass(generateAdapter = true)
data class ArtistAppearance(
    val id: String,
    val title: String = "",
    val role: String = "",
    val album_id: String,
    val album_title: String = "",
    val album_year: Int? = null,
    val album_poster_path: String? = null,
    val album_artist_id: String? = null,
    val album_artist_title: String? = null,
)

/** GET /music/mix?seed=<track|album|artist id>: one track of an instant mix,
 *  in play order (a seed track first). */
@JsonClass(generateAdapter = true)
data class MixTrack(
    val id: String,
    val title: String = "",
    val duration_ms: Long? = null,
    val index: Int? = null,
    val album_id: String = "",
    val album_title: String = "",
    val album_poster_path: String? = null,
    val artist_id: String? = null,
    val artist_title: String? = null,
    val file_id: String = "",
)
