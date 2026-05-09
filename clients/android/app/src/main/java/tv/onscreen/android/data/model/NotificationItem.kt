package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/**
 * A single SSE message off /api/v1/notifications/stream. The server reuses
 * one channel for both user-facing notifications (item_added etc., title +
 * body populated) and cross-device sync events (`progress.updated` etc.,
 * data populated). Consumers branch on [type] before deciding what to do
 * with the row — see [tv.onscreen.android.data.api.NotificationsStream].
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
    /** Raw payload — server emits one `data` field per event but its
     *  shape varies by `type` (`progress.updated`, `playback.transfer`,
     *  etc.). Decoded loosely so a new event type doesn't crash the
     *  deserializer; consumers branch on `type` and pull typed views
     *  via the helpers below.
     *
     *  Was previously typed as `ProgressUpdateData?` which threw on
     *  any non-progress event (the v2.2 `playback.transfer` event has
     *  no `state` field, so Moshi-codegen rejected it). The Map-typed
     *  field is forward-compatible. */
    val data: Map<String, Any?>? = null,
)

/** Helpers turning `NotificationItem.data` into typed payloads. They
 *  return null when the shape doesn't match — consumers must check
 *  `type` first and only then call the matching extractor. */
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

fun NotificationItem.asPlaybackTransfer(): PlaybackTransferData? {
    val d = data ?: return null
    val itemId = d["item_id"] as? String ?: return null
    val target = d["target_client_name"] as? String ?: return null
    return PlaybackTransferData(
        item_id = itemId,
        position_ms = (d["position_ms"] as? Number)?.toLong() ?: 0L,
        target_client_name = target,
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

/** Payload of a `playback.transfer` SSE event (v2.2). Server emits one
 *  per call to POST /api/v1/playback/transfer; the receiving TV that
 *  matches `target_client_name` loads the item and seeks to
 *  `position_ms` before playing. */
data class PlaybackTransferData(
    val item_id: String,
    val position_ms: Long = 0L,
    val target_client_name: String,
)

/** Body for POST /api/v1/playback/transfer — initiated from a phone or
 *  another TV asking the named target device to take over playback.
 *  TV clients typically only RECEIVE transfers (via the SSE event),
 *  but having the type lets the user transfer FROM the TV to another
 *  device too. */
@JsonClass(generateAdapter = true)
data class PlaybackTransferRequest(
    val item_id: String,
    val position_ms: Long = 0L,
    val target_client_name: String,
)

@JsonClass(generateAdapter = true)
data class UnreadCount(val count: Long)
