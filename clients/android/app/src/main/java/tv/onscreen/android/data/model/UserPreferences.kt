package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class UserPreferences(
    val preferred_audio_lang: String? = null,
    val preferred_subtitle_lang: String? = null,
    val forced_subtitles_only: Boolean = false,
    val max_content_rating: String? = null,
    // Per-user home (hub) row order + visibility, configured on the web home page
    // and shared across devices via the user prefs. null = never customized
    // (render the default layout). Keys the clients understand: continue_tv,
    // continue_movies, continue_other, trending, library:<uuid>. The regular prefs
    // PUT ignores this field (it's written via PUT /users/me/hub-layout), so
    // round-tripping it on a settings save is harmless.
    val hub_layout: List<HubRowPref>? = null,
)

/** One entry of the saved hub layout: a row [key] and whether it's shown. */
@JsonClass(generateAdapter = true)
data class HubRowPref(
    val key: String,
    val enabled: Boolean = true,
)
