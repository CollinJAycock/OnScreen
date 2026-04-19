package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class HistoryItem(
    val id: String,
    val media_id: String,
    val title: String,
    val type: String,
    val year: Int? = null,
    val thumb_path: String? = null,
    val client_name: String? = null,
    val duration_ms: Long? = null,
    val occurred_at: String,
)
