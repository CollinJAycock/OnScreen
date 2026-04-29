package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

/**
 * A single SSE message off /api/v1/notifications/stream. The server reuses
 * one channel for both user-facing notifications (item_added etc., title +
 * body populated) and cross-device sync events (`progress.updated` etc.,
 * data populated). Consumers branch on [type] before deciding what to do
 * with the row — see [tv.onscreen.mobile.data.api.NotificationsStream].
 *
 * `created_at` is the server's UnixMilli timestamp (int64 in JSON) — it
 * was previously typed as String which silently dropped every payload
 * during Moshi parse.
 */
@JsonClass(generateAdapter = true)
data class NotificationItem(
    val id: String = "",
    val type: String,
    val title: String = "",
    val body: String = "",
    val item_id: String? = null,
    val read: Boolean = false,
    val created_at: Long = 0L,
    val data: ProgressUpdateData? = null,
)

/** Payload of a `progress.updated` SSE event. Mirrors the server-side
 *  struct in internal/api/v1/items.go's Progress handler. */
@JsonClass(generateAdapter = true)
data class ProgressUpdateData(
    val item_id: String,
    val position_ms: Long,
    val duration_ms: Long = 0L,
    val state: String, // "playing" | "paused" | "stopped"
)

@JsonClass(generateAdapter = true)
data class UnreadCount(val count: Long)
