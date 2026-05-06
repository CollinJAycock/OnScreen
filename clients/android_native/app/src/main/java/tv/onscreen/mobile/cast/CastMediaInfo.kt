package tv.onscreen.mobile.cast

import java.net.URLEncoder

/**
 * Pure helpers for Google Cast LOAD-message construction.
 *
 * Mirrors the web client's `web/src/lib/cast.ts`:
 *   - same MIME-mapping table (Default Media Receiver compat set)
 *   - same metadata-type integers (Cast protocol constants)
 *   - same `isCastable` predicate (active + stream_token + supported MIME)
 *   - same URL build (absolute origin + tokenised /media/stream/{id})
 *
 * Zero Android imports — Cast Framework integration lives in
 * `CastSessionManager` and friends. This module is unit-testable in the
 * JVM sourceset.
 */
object CastMediaInfo {

    /** Cast metadata-type constants. Values are part of the Cast
     *  protocol — never change them. */
    const val METADATA_TYPE_GENERIC: Int = 0
    const val METADATA_TYPE_MOVIE: Int = 1
    const val METADATA_TYPE_TV_SHOW: Int = 2
    const val METADATA_TYPE_MUSIC_TRACK: Int = 3
    const val METADATA_TYPE_PHOTO: Int = 4

    /** Stream-type string used in the LOAD message's `streamType`
     *  field. Always BUFFERED for VOD; LIVE / NONE are out of scope. */
    const val STREAM_TYPE_BUFFERED: String = "BUFFERED"

    /** Minimal item shape for the helpers. Phone passes ItemDetail
     *  through this projection so tests don't need a full model. */
    data class Item(
        val id: String,
        val type: String,
        val title: String,
        val posterPath: String? = null,
        val parentTitle: String? = null,
    )

    /** Minimal file shape — same posture as Item. */
    data class File(
        val id: String,
        val status: String,
        val container: String? = null,
        val videoCodec: String? = null,
        val audioCodec: String? = null,
        val durationSeconds: Long? = null,
        val streamToken: String? = null,
    )

    /** Result of building a LOAD payload. The Cast SDK wants this in
     *  `MediaInfo` / `MediaMetadata` form — we pass the data through
     *  `buildMediaInfo` in the Android-only sender. */
    data class Payload(
        val contentId: String,
        val contentType: String,
        val streamType: String,
        val metadataType: Int,
        val title: String,
        val subtitle: String?,
        val imageUrls: List<String>,
        val durationSeconds: Long?,
        val customData: Map<String, String>,
    )

    // ── pure helpers ──────────────────────────────────────────────────────

    /**
     * Map container + codec → Cast-friendly MIME. Default Receiver's
     * documented support: H.264/HEVC mp4 + AAC, plus audio-only mp3 /
     * aac / flac / ogg. Anything else returns "" and the UI hides the
     * Cast button rather than offering it and failing.
     */
    fun contentType(file: File): String {
        val ct = (file.container ?: "").lowercase()
        val vc = (file.videoCodec ?: "").lowercase()
        val ac = (file.audioCodec ?: "").lowercase()

        // Audio-only.
        if (vc.isEmpty() && ac.isNotEmpty()) {
            if (ct == "mp3" || ac == "mp3") return "audio/mp3"
            if (ct == "m4a" || ct == "mp4" || ac == "aac" || ac.startsWith("mp4a")) return "audio/mp4"
            if (ct == "flac") return "audio/flac"
            if (ct == "ogg" || ac == "opus" || ac == "vorbis") return "audio/ogg"
            return ""
        }

        // Video. Restrict to documented Default-Receiver containers;
        // mkv works in practice but isn't supported, so we don't claim it.
        if (vc != "h264" && vc != "hevc" && vc != "h265") return ""
        return when (ct) {
            "mp4", "m4v", "mov" -> "video/mp4"
            "webm" -> "video/webm"
            else -> ""
        }
    }

    /** Map OnScreen item.type to Cast metadata-type integer. Unknown
     *  types fall through to GENERIC so the receiver still renders
     *  the title + poster. */
    fun metadataType(item: Item): Int = when (item.type) {
        "movie" -> METADATA_TYPE_MOVIE
        "episode", "show", "season" -> METADATA_TYPE_TV_SHOW
        "track", "music" -> METADATA_TYPE_MUSIC_TRACK
        "photo" -> METADATA_TYPE_PHOTO
        else -> METADATA_TYPE_GENERIC
    }

    /** Predicate: is this file eligible for v1 Cast (direct play with
     *  a receiver-compatible codec)? Drives whether the UI renders a
     *  Cast button. */
    fun isCastable(file: File): Boolean {
        if (file.status != "active") return false
        if (file.streamToken.isNullOrEmpty()) return false
        return contentType(file).isNotEmpty()
    }

    /**
     * Build the LOAD payload. baseUrl is the absolute origin (e.g.
     * "https://onscreen.example") because Cast devices fetch on the
     * LAN directly — they need an absolute URL, not the relative one
     * the client uses internally.
     *
     * Returns null if the file isn't castable.
     */
    fun build(item: Item, file: File, baseUrl: String): Payload? {
        if (!isCastable(file)) return null
        val origin = baseUrl.trimEnd('/')
        val token = file.streamToken!!
        val url = "$origin/media/stream/${urlEncode(file.id)}?token=${urlEncode(token)}"
        val type = metadataType(item)
        val images = if (item.posterPath != null) listOf("$origin${item.posterPath}") else emptyList()
        return Payload(
            contentId = url,
            contentType = contentType(file),
            streamType = STREAM_TYPE_BUFFERED,
            metadataType = type,
            title = item.title,
            subtitle = item.parentTitle,
            imageUrls = images,
            durationSeconds = file.durationSeconds,
            customData = mapOf("itemId" to item.id, "fileId" to file.id),
        )
    }

    private fun urlEncode(s: String): String =
        URLEncoder.encode(s, "UTF-8").replace("+", "%20")
}
