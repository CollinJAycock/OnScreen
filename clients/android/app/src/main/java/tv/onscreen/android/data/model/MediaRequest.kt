package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/**
 * Server-side media request — a user's "please add this to the
 * library" record. Created via POST /api/v1/requests, fulfilled
 * by an admin who routes it to the configured Sonarr/Radarr.
 *
 * The Android client only needs the create + status-check shapes;
 * approve/decline/delete are admin actions exposed through the web
 * UI today.
 */
@JsonClass(generateAdapter = true)
data class MediaRequest(
    val id: String,
    val user_id: String,
    val type: String, // "movie" | "show"
    val tmdb_id: Int,
    val title: String,
    val year: Int? = null,
    val poster_url: String? = null,
    val overview: String? = null,
    /** "pending" | "approved" | "declined" | "downloading" |
     *  "available" | "failed". UI maps each to a chip colour. */
    val status: String,
    val created_at: String? = null,
    val updated_at: String? = null,
)

@JsonClass(generateAdapter = true)
data class CreateRequestBody(
    val type: String, // "movie" | "show"
    val tmdb_id: Int,
)
