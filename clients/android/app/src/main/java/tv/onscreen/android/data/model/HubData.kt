package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class HubData(
    val continue_watching: List<HubItem>,
    val recently_added: List<HubItem>,
)

@JsonClass(generateAdapter = true)
data class HubItem(
    val id: String,
    val title: String,
    val type: String,
    val year: Int? = null,
    val poster_path: String? = null,
    val fanart_path: String? = null,
    val thumb_path: String? = null,
    val view_offset_ms: Long? = null,
    val duration_ms: Long? = null,
    val updated_at: Long = 0,
)
