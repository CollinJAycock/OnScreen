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
    /** v2.5 per-user watch fields on the library listing. All optional —
     *  older servers (and non-video types) omit them, and the card then
     *  renders no watch indicator.
     *
     *  [watch_state]: "watched" | "in_progress" | "unwatched" on playable
     *  videos (movie, episode, music video, home video). */
    val watch_state: String? = null,
    /** Resume point for an in-progress video. */
    val view_offset_ms: Long? = null,
    /** Shows / seasons: episodes within the caller's rating ceiling, and how
     *  many of them are not yet watched. */
    val leaf_count: Long? = null,
    val unwatched_count: Long? = null,
)
