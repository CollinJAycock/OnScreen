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
)
