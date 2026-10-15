package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

// Music discovery, v2.6 — internal/api/v1/music.go. All three answer the list
// envelope ({data:[…], meta}); 404 on an older server.

/** One entry of GET /music/artists/{id}/similar. */
@JsonClass(generateAdapter = true)
data class SimilarArtist(
    val id: String,
    val title: String,
    val poster_path: String? = null,
    val updated_at: Long = 0,
    /** Similarity, 0..1. */
    val score: Double = 0.0,
    val shared_genres: List<String> = emptyList(),
    /** People who listen to one listen to the other. */
    val co_listened: Boolean = false,
)

/** One track of GET /music/artists/{id}/appears-on: a track this artist is
 *  credited on, on an album by someone else. The page groups them by album. */
@JsonClass(generateAdapter = true)
data class ArtistAppearance(
    val id: String,
    val title: String,
    val index: Int? = null,
    val disc_number: Int? = null,
    val duration_ms: Long? = null,
    /** "featured" (a feat. credit) or "artist" (e.g. a compilation track). */
    val role: String = "",
    val album_id: String,
    val album_title: String = "",
    val album_year: Int? = null,
    val album_poster_path: String? = null,
    val album_updated_at: Long = 0,
    val album_artist_id: String? = null,
    val album_artist_title: String? = null,
)

/** One track of GET /music/mix?seed=…: what a queue needs to play it. */
@JsonClass(generateAdapter = true)
data class MixTrack(
    val id: String,
    val title: String,
    val duration_ms: Long? = null,
    val index: Int? = null,
    val album_id: String = "",
    val album_title: String = "",
    val album_poster_path: String? = null,
    val artist_id: String? = null,
    val artist_title: String? = null,
    val file_id: String = "",
)
