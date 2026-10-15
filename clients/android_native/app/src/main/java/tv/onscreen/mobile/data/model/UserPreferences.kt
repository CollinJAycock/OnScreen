package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class UserPreferences(
    val preferred_audio_lang: String? = null,
    val preferred_subtitle_lang: String? = null,
    val forced_subtitles_only: Boolean = false,
    val max_content_rating: String? = null,
    // The user's own playback-quality profile (v2.6), enforced by the server
    // on every device. Written through PUT /users/me/quality-profile, which
    // REPLACES all five (forced_subtitles_only included) — see
    // QualityProfile. Null = no limit / automatic; absent on older servers.
    val max_video_height: Int? = null,
    val max_video_bitrate_kbps: Int? = null,
    val max_audio_bitrate_kbps: Int? = null,
    /** "h264" | "hevc" | null (automatic). */
    val preferred_video_codec: String? = null,
    // Per-user home (hub) row order + visibility, configured on the web home page
    // and shared across devices via the user prefs. null = never customized
    // (render the default layout). Keys: continue_tv, continue_movies,
    // continue_other, next_up, plan_to_watch, trending, library:<uuid>,
    // libraries (the server back-fills next_up / plan_to_watch into layouts
    // saved before those rows existed). The regular prefs PUT
    // ignores this field (it's written via PUT /users/me/hub-layout), so
    // round-tripping it on a settings save is harmless.
    val hub_layout: List<HubRowPref>? = null,
)

/** Body for PUT /users/me/quality-profile (v2.6). The endpoint REPLACES the
 *  whole profile — a null (omitted) limit is cleared — so every save sends all
 *  five, built by QualityProfile.request from a fresh read. */
@JsonClass(generateAdapter = true)
data class QualityProfileRequest(
    val max_video_height: Int?,
    val max_video_bitrate_kbps: Int?,
    val max_audio_bitrate_kbps: Int?,
    val preferred_video_codec: String?,
    val forced_subtitles_only: Boolean,
)

/** One entry of the saved hub layout: a row [key] and whether it's shown. */
@JsonClass(generateAdapter = true)
data class HubRowPref(
    val key: String,
    val enabled: Boolean = true,
)
