package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/**
 * A user's parental watch policy plus today's usage and whether playback is
 * allowed right now. The three limit fields are null when unrestricted;
 * [remaining_minutes] is present only when a daily cap is set; [reason] is set
 * only when [allowed] is false (`daily_limit_reached` | `outside_allowed_hours`).
 */
@JsonClass(generateAdapter = true)
data class WatchLimitData(
    val daily_limit_minutes: Int?,
    val allowed_start_minute: Int?,
    val allowed_end_minute: Int?,
    val used_minutes_today: Int,
    val remaining_minutes: Int?,
    val allowed: Boolean,
    val reason: String?,
)
