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
    /** v2.6 extra artwork, served through /artwork/ like [poster_path]: a
     *  transparent clear logo (PNG — fetch it WITHOUT ?w=, which re-encodes
     *  as JPEG and loses the transparency), a wide banner and disc art.
     *  Absent when the item has none, and from older servers. */
    val logo_path: String? = null,
    val banner_path: String? = null,
    val disc_art_path: String? = null,
    val content_rating: String? = null,
    val genres: List<String> = emptyList(),
    /** A movie's production companies or a show's networks (v2.6), in the
     *  provider's order. Empty when none, and from older servers. */
    val studios: List<ItemStudio> = emptyList(),
    val parent_id: String? = null,
    val index: Int? = null,
    /** The last episode a multi-episode file covers: an "S01E01-E02" file is
     *  ONE item with [index] 1 and index_end 2. Null for a single episode,
     *  every other type, and older servers (see EpisodeNumbering). */
    val index_end: Int? = null,
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
    /** Pre-made MP4 copies (Optimize…, v2.6), highest bitrate first. Never
     *  part of [files]; the play decision hands one out (see
     *  PlaybackDecision.optimized_version). */
    val optimized_versions: List<OptimizedVersion> = emptyList(),
)

/** One studio (movies) or network (shows) on an item. */
@JsonClass(generateAdapter = true)
data class ItemStudio(
    val id: String = "",
    val name: String,
    /** "studio" | "network". */
    val kind: String? = null,
)

/** A playable optimized version of an item (internal/api/v1/optimize.go
 *  OptimizedVersionJSON): always an MP4 with H.264 video and stereo AAC. */
@JsonClass(generateAdapter = true)
data class OptimizedVersion(
    val id: String,
    /** The source file it was made from (a stacked movie's first part — the
     *  version is the whole movie). */
    val file_id: String = "",
    val profile: String = "",
    val label: String = "",
    val container: String? = null,
    val video_codec: String? = null,
    val audio_codec: String? = null,
    val width: Int? = null,
    val height: Int? = null,
    val bitrate_kbps: Int? = null,
    val duration_ms: Long? = null,
    /** Server-relative `/media/optimized/{id}`. */
    val stream_url: String,
    /** 24 h token bound to this version, for a player that authenticates
     *  through the URL — like ItemFile.stream_token. */
    val stream_token: String? = null,
)
