package tv.onscreen.mobile.data.model

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
    // The caller's watch state (v2.5; all absent on older servers or when
    // the server's watch store isn't wired). Videos (movie / episode /
    // music & home video): watch_state + view_offset_ms (only with a
    // resume point). Shows / seasons: leaf_count = episodes within the
    // rating ceiling, unwatched_count = how many of those aren't watched.
    val watch_state: String? = null,
    val view_offset_ms: Long? = null,
    val leaf_count: Long? = null,
    val unwatched_count: Long? = null,
)
