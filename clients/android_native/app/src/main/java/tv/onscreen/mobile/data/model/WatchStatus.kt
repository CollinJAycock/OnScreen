package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

/**
 * Watching-status enumeration mirroring the v2.2 anime-track server
 * model. Five values: `plan_to_watch`, `watching`, `on_hold`,
 * `completed`, `dropped`.
 *
 * Wire format is the lowercase snake_case string — same shape the
 * web client sees. Sealed enum so the UI dropdown can iterate values
 * without keeping a separate label list.
 */
enum class WatchStatus(val wire: String) {
    PLAN_TO_WATCH("plan_to_watch"),
    WATCHING("watching"),
    ON_HOLD("on_hold"),
    COMPLETED("completed"),
    DROPPED("dropped");

    companion object {
        /** Tolerant parser — unknown values yield null so the UI can
         *  fall back to "no status" instead of throwing. */
        fun fromWire(s: String?): WatchStatus? =
            values().firstOrNull { it.wire == s }
    }
}

/** Request body for `PUT /items/{id}/watch-status`. */
@JsonClass(generateAdapter = true)
data class WatchStatusRequest(
    val status: String,
)

/** Response from `GET / PUT /items/{id}/watch-status`. */
@JsonClass(generateAdapter = true)
data class WatchStatusResponse(
    val status: String,
    val created_at: String,
    val updated_at: String,
)
