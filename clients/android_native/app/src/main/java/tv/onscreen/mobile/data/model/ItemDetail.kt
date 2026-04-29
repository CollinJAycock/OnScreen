package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class ItemDetail(
    val id: String,
    val library_id: String,
    val title: String,
    val type: String,
    val year: Int? = null,
    val summary: String? = null,
    val rating: Double? = null,
    val duration_ms: Long? = null,
    val poster_path: String? = null,
    val fanart_path: String? = null,
    val content_rating: String? = null,
    val genres: List<String> = emptyList(),
    val parent_id: String? = null,
    val index: Int? = null,
    val view_offset_ms: Long = 0,
    val updated_at: Long = 0,
    val is_favorite: Boolean = false,
    val files: List<ItemFile> = emptyList(),
)
