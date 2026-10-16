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
        // WHY: content-absolute resume position, in seconds, handed to the
        // receiver so a hand-off continues from the local playhead instead
        // of restarting at 0:00. Double keeps sub-second accuracy and
        // mirrors the web client's LoadRequest.currentTime (also seconds).
        val positionSeconds: Double,
        val customData: Map<String, String>,
        /** HLS only: the segment container to tell the receiver
         *  (MediaInfo hlsSegmentFormat / hlsVideoSegmentFormat). Null for a
         *  direct cast of the file. */
        val hlsSegmentFormat: CastHls.SegmentFormat? = null,
        /** The media has a picture: the HLS video segment format is set too. */
        val hasVideo: Boolean = true,
        /** Sidecar WebVTT tracks; the first is made active. */
        val textTracks: List<CastHls.TextTrack> = emptyList(),
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
    fun build(item: Item, file: File, baseUrl: String, positionSeconds: Double = 0.0): Payload? {
        if (!isCastable(file)) return null
        val origin = baseUrl.trimEnd('/')
        val token = file.streamToken!!
        val url = "$origin/media/stream/${urlEncode(file.id)}?token=${urlEncode(token)}"
        val type = metadataType(item)
        // WHY: posterPath is a bare, server-relative artwork path — no
        // leading slash, no /artwork prefix, and it may contain spaces or
        // sub-directories. The old "$origin$posterPath" fused the origin
        // straight onto the path (no separator, unencoded), yielding a
        // malformed URL. Mirror data/artworkUrl()'s output shape,
        // "{origin}/artwork/{per-segment-encoded}?w=", encoding each
        // segment while preserving '/'. We can't call artworkUrl() itself:
        // it uses android.net.Uri, and this module is deliberately
        // Android-free so its unit tests run on the plain JVM.
        val images = posterUrls(item, origin)
        return Payload(
            contentId = url,
            contentType = contentType(file),
            streamType = STREAM_TYPE_BUFFERED,
            metadataType = type,
            title = item.title,
            subtitle = item.parentTitle,
            imageUrls = images,
            durationSeconds = file.durationSeconds,
            positionSeconds = positionSeconds,
            customData = mapOf("itemId" to item.id, "fileId" to file.id),
        )
    }

    /**
     * The LOAD payload for an OnScreen HLS session. [playlistUrl] is the
     * start response's playlist_url made absolute (it carries its own
     * `?token=`); [offsetMs] is the content time the session opens at, and
     * [receiverStartMs] where in the SESSION the receiver starts. The
     * stream's duration is what remains of the content after [offsetMs].
     */
    fun buildHls(
        item: Item,
        fileId: String,
        playlistUrl: String,
        segmentFormat: CastHls.SegmentFormat,
        hasVideo: Boolean,
        contentDurationMs: Long?,
        offsetMs: Long,
        receiverStartMs: Long,
        sessionId: String?,
        baseUrl: String,
        textTrack: CastHls.TextTrack? = null,
    ): Payload {
        val remaining = contentDurationMs?.takeIf { it > 0 }?.let { (it - offsetMs).coerceAtLeast(0) }
        val custom = buildMap {
            put("itemId", item.id)
            put("fileId", fileId)
            put("offsetMs", offsetMs.toString())
            sessionId?.takeIf { it.isNotEmpty() }?.let { put("sessionId", it) }
        }
        return Payload(
            contentId = playlistUrl,
            contentType = CastHls.CONTENT_TYPE_HLS,
            streamType = STREAM_TYPE_BUFFERED,
            metadataType = metadataType(item),
            title = item.title,
            subtitle = item.parentTitle,
            imageUrls = posterUrls(item, baseUrl.trimEnd('/')),
            durationSeconds = remaining?.let { it / 1000L },
            positionSeconds = receiverStartMs.coerceAtLeast(0) / 1000.0,
            customData = custom,
            hlsSegmentFormat = segmentFormat,
            hasVideo = hasVideo,
            textTracks = listOfNotNull(textTrack),
        )
    }

    private fun posterUrls(item: Item, origin: String): List<String> =
        if (item.posterPath != null) {
            val encodedPath = item.posterPath.split("/").joinToString("/") { urlEncode(it) }
            listOf("$origin/artwork/$encodedPath?w=500")
        } else {
            emptyList()
        }

    private fun urlEncode(s: String): String =
        URLEncoder.encode(s, "UTF-8").replace("+", "%20")
}
