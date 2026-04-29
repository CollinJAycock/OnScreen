package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/** A tunable channel from /tv/channels. The TV client lists every
 *  enabled channel in a Leanback grid; the disabled ones (curated
 *  via the web settings UI) never come back from the list endpoint
 *  with `enabled_only=true`. */
@JsonClass(generateAdapter = true)
data class Channel(
    val id: String,
    val tuner_id: String,
    val tuner_name: String,
    val tuner_type: String,
    val number: String,
    val callsign: String? = null,
    val name: String,
    val logo_url: String? = null,
    val enabled: Boolean = true,
    val sort_order: Int = 0,
    val epg_channel_id: String? = null,
)

/** Current + next program for a channel from /tv/channels/now-next.
 *  The endpoint returns up to two rows per channel (current first,
 *  then next). Channels with no EPG data don't appear at all — the
 *  client merges by channel_id and renders "no guide data" for
 *  missing channels. */
@JsonClass(generateAdapter = true)
data class NowNext(
    val channel_id: String,
    val program_id: String,
    val title: String,
    val subtitle: String? = null,
    val starts_at: String, // ISO8601
    val ends_at: String,
    val season_num: Int? = null,
    val episode_num: Int? = null,
)

/** A completed or scheduled DVR recording from /tv/recordings.
 *  Status: "scheduled" | "recording" | "completed" | "failed" |
 *  "cancelled". When status=completed and item_id is set, the
 *  recording landed in a media_item and can be played back via
 *  the standard /items/{item_id} → /watch flow. */
@JsonClass(generateAdapter = true)
data class Recording(
    val id: String,
    val schedule_id: String? = null,
    val channel_id: String,
    val channel_number: String,
    val channel_name: String,
    val channel_logo: String? = null,
    val program_id: String? = null,
    val title: String,
    val subtitle: String? = null,
    val season_num: Int? = null,
    val episode_num: Int? = null,
    val status: String,
    val starts_at: String,
    val ends_at: String,
    val item_id: String? = null,
    val error: String? = null,
)
