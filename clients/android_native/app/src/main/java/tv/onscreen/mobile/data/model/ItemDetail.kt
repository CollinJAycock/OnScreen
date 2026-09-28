package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class ItemDetail(
    val id: String,
    val library_id: String,
    val title: String,
    val type: String,
    val year: Int? = null,
    val summary: String? = null,
    val rating: Double? = null,
    val duration_ms: Long? = null,
    val poster_path: String? = null,
    val fanart_path: String? = null,
    val content_rating: String? = null,
    val genres: List<String> = emptyList(),
    val parent_id: String? = null,
    val index: Int? = null,
    val view_offset_ms: Long = 0,
    /** The caller's state for a playable video — "watched" / "in_progress"
     *  / "unwatched" (manual marks included). Absent for other types and on
     *  pre-v2.5 servers; shows / seasons roll up via up-next instead. */
    val watch_state: String? = null,
    val updated_at: Long = 0,
    val is_favorite: Boolean = false,
    /** Book-only: 'ltr', 'rtl' (manga), or 'ttb' (webtoon). Populated by
     *  the manga enricher from AniList countryOfOrigin; null for ordinary
     *  Western books and every non-book item. */
    val reading_direction: String? = null,
    val files: List<ItemFile> = emptyList(),
)
