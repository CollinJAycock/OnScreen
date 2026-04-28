package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/**
 * Response shape of GET /api/v1/items/{id}/trickplay. The status
 * field gates whether the .vtt + sprite endpoints are worth hitting:
 * only `done` items have generated thumbnails. Everything else means
 * the player should fall back to position-only seek (no preview).
 *
 * sprite_count + interval_sec + thumb_width/height are diagnostic
 * fields the player doesn't strictly need (the .vtt carries the
 * authoritative coordinates for each cue) but are surfaced for
 * future "regenerate trickplay" admin UX.
 */
@JsonClass(generateAdapter = true)
data class TrickplayStatus(
    val status: String, // not_started | pending | done | failed | skipped
    val sprite_count: Int = 0,
    val interval_sec: Int = 0,
    val thumb_width: Int = 0,
    val thumb_height: Int = 0,
    val last_error: String = "",
)

/**
 * One cue parsed from /trickplay/{id}/index.vtt. The VTT payload
 * for each cue looks like `sprite_000.jpg#xywh=0,0,320,180` — file
 * is the sprite-sheet filename relative to /trickplay/{id}/, and
 * x/y/w/h are the crop region within that sheet.
 */
data class TrickplayCue(
    val startMs: Long,
    val endMs: Long,
    val file: String,
    val x: Int,
    val y: Int,
    val w: Int,
    val h: Int,
)
