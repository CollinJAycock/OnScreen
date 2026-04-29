package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class Chapter(
    val title: String,
    val start_ms: Long,
    val end_ms: Long,
)
