package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class Library(
    val id: String,
    val name: String,
    val type: String,
    val created_at: String,
    val updated_at: String,
)
