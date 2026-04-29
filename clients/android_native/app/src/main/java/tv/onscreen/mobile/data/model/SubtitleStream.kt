package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class SubtitleStream(
    val index: Int,
    val codec: String,
    val language: String,
    val title: String,
    val forced: Boolean,
)
