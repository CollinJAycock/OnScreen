package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class AudioStream(
    val index: Int,
    val codec: String,
    val channels: Int,
    val language: String,
    val title: String,
)
