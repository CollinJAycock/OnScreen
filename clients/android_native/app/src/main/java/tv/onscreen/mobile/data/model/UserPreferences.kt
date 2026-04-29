package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class UserPreferences(
    val preferred_audio_lang: String? = null,
    val preferred_subtitle_lang: String? = null,
    val max_content_rating: String? = null,
)
