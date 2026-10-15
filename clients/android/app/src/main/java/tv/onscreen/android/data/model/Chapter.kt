package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class Chapter(
    val title: String,
    val start_ms: Long,
    val end_ms: Long,
    /** v2.6. The chapter's thumbnail (/trickplay/{item_id}/chapter_NNN.jpg),
     *  once the item's seek-bar sprites exist. Server-relative; the shared
     *  image loader's bearer authenticates it. Null otherwise and from older
     *  servers. */
    val thumb_url: String? = null,
)
