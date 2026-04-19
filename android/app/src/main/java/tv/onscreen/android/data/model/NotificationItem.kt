package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class NotificationItem(
    val id: String,
    val type: String,
    val title: String,
    val body: String = "",
    val item_id: String? = null,
    val read: Boolean = false,
    val created_at: String,
)

@JsonClass(generateAdapter = true)
data class UnreadCount(val count: Long)
