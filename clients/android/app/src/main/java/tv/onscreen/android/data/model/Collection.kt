package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class MediaCollection(
    val id: String,
    val name: String,
    val description: String? = null,
    val type: String,
    val genre: String? = null,
    val poster_path: String? = null,
    val created_at: String,
)

@JsonClass(generateAdapter = true)
data class CollectionItem(
    val id: String,
    val title: String,
    val type: String,
    val year: Int? = null,
    val rating: Double? = null,
    val poster_path: String? = null,
    val duration_ms: Long? = null,
    val position: Int? = null,
)
