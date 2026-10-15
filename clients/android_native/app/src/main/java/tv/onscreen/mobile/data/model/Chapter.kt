package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class Chapter(
    val title: String,
    val start_ms: Long,
    val end_ms: Long,
    /** Server-relative chapter thumbnail (`/trickplay/{item}/chapter_NNN.jpg`,
     *  v2.6), once the item's seek-bar sprites exist. Bearer-authenticated
     *  like the sprites, so the shared Coil/OkHttp stack can load it. */
    val thumb_url: String? = null,
)
