package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class FavoriteItem(
    val id: String,
    val library_id: String,
    val type: String,
    val title: String,
    val year: Int? = null,
    val summary: String? = null,
    val poster_path: String? = null,
    val thumb_path: String? = null,
    val duration_ms: Long? = null,
    val favorited_at: Long = 0,
)
