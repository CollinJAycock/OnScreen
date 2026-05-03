package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class MediaItem(
    val id: String,
    val title: String,
    val type: String,
    val year: Int? = null,
    val summary: String? = null,
    val rating: Double? = null,
    val duration_ms: Long? = null,
    val genres: List<String>? = null,
    val poster_path: String? = null,
    val created_at: String,
    val updated_at: String,
    /** v2.1. Date the content was originally produced — EXIF
     *  DateTimeOriginal for photos, file mtime for home videos
     *  (drives the date-grouped grid + the "Resume from <date>"
     *  affordance), TMDB release date for movies + episodes,
     *  null for items where it's meaningless (audio tracks etc.).
     *  RFC3339 string. Older server builds omit it. */
    val originally_available_at: String? = null,
)
