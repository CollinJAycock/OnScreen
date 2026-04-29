package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class SearchResult(
    val id: String,
    val library_id: String,
    val title: String,
    val type: String,
    val year: Int? = null,
    val poster_path: String? = null,
    val thumb_path: String? = null,
)
