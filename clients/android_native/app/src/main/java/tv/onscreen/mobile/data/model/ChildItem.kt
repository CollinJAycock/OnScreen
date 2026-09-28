package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class ChildItem(
    val id: String,
    val title: String,
    val type: String,
    val year: Int? = null,
    val summary: String? = null,
    val rating: Double? = null,
    val duration_ms: Long? = null,
    val poster_path: String? = null,
    val thumb_path: String? = null,
    val index: Int? = null,
    /** A track's disc within its album; [index] is its number on that disc.
     *  Null reads as disc 1: single-disc albums, every non-track child, and
     *  servers that predate the field. */
    val disc_number: Int? = null,
    val view_offset_ms: Long = 0,
    val watched: Boolean = false,
    val created_at: String? = null,
    val updated_at: Long = 0,
) {
    companion object {
        /** Play order within a container: disc, then [index], unnumbered rows
         *  last. The server lists an album's tracks in this order. Only tracks
         *  carry a disc, so for any other type it is plain index order. */
        val PLAY_ORDER: Comparator<ChildItem> =
            compareBy({ it.disc_number ?: 1 }, { it.index ?: Int.MAX_VALUE })
    }
}
