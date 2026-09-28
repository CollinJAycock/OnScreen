package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

/**
 * A single SSE message off /api/v1/notifications/stream. The server reuses
 * one channel for both user-facing notifications (item_added etc., title +
 * body populated) and cross-device sync events (`progress.updated`,
 * `playback.stop`, …, data populated). Consumers branch on [type] before
 * deciding what to do with the row — see
 * [tv.onscreen.mobile.data.api.NotificationsStream].
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
    /** Raw payload — its shape varies by [type]. Decoded loosely so a new
     *  event type can't break the deserializer; consumers check [type] and
     *  then pull a typed view via the extractors below.
     *
     *  Was previously typed as `ProgressUpdateData?`, which made Moshi
     *  reject every event whose payload lacked `position_ms`/`state` — the
     *  admin `playback.stop` event among them. Same shape as the TV
     *  client's NotificationItem. */
    val data: Map<String, Any?>? = null,
)

/** Notification types this app must never surface or act on: media requests
 *  (`request_created`, `request_approved`, …) are not a feature of the
 *  Android apps. Dropped at the SSE parse layer
 *  ([tv.onscreen.mobile.data.api.NotificationsStream]) so no subscriber —
 *  present or future — can ever see one. Same rule as the TV client. */
fun isHiddenNotificationType(type: String): Boolean =
    type.startsWith("request_") || type.startsWith("request.")

/** Typed view of a `progress.updated` payload; null when the shape doesn't
 *  match. Check [NotificationItem.type] first. */
fun NotificationItem.asProgressUpdate(): ProgressUpdateData? {
    val d = data ?: return null
    val itemId = d["item_id"] as? String ?: return null
    val pos = (d["position_ms"] as? Number)?.toLong() ?: return null
    val state = d["state"] as? String ?: return null
    return ProgressUpdateData(
        item_id = itemId,
        position_ms = pos,
        duration_ms = (d["duration_ms"] as? Number)?.toLong() ?: 0L,
        state = state,
    )
}

/** Typed view of a `playback.stop` payload (internal/api/v1/playback_stop.go
 *  PlaybackStopPayload); null when it isn't a usable stop event. Empty
 *  strings count as absent, as on the web client. */
fun NotificationItem.asPlaybackStop(): PlaybackStopEvent? {
    val d = data ?: return null
    fun str(key: String): String? = (d[key] as? String)?.takeIf { it.isNotEmpty() }
    val itemId = str("item_id") ?: return null
    return PlaybackStopEvent(
        item_id = itemId,
        session_id = str("session_id"),
        client_name = str("client_name"),
        decision = str("decision"),
        message = str("message"),
    )
}

/** Payload of a `progress.updated` SSE event. Mirrors the server-side
 *  struct in internal/api/v1/items.go's Progress handler. */
data class ProgressUpdateData(
    val item_id: String,
    val position_ms: Long,
    val duration_ms: Long = 0L,
    val state: String, // "playing" | "paused" | "stopped"
)

@JsonClass(generateAdapter = true)
data class UnreadCount(val count: Long)
