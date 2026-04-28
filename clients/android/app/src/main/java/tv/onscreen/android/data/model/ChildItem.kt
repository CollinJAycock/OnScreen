package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class ChildItem(
    val id: String,
    val title: String,
    val type: String,
    val year: Int? = null,
    val summary: String? = null,
    val rating: Double? = null,
    val duration_ms: Long? = null,
    val poster_path: String? = null,
    val thumb_path: String? = null,
    val index: Int? = null,
    val view_offset_ms: Long = 0,
    val watched: Boolean = false,
    val created_at: String? = null,
    val updated_at: Long = 0,
)
