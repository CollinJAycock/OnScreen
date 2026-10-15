package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

// Converted (smaller) downloads, v2.6 — internal/api/v1/downloads.go. The
// original file still downloads as it always has; these are lower-quality MP4
// copies the server encodes on request, once per file and quality.

/** GET /items/{id}/downloads[?file_id=]: the qualities on offer. [file_id] is
 *  the file a conversion encodes (a stacked movie's first part — the copy is
 *  the whole movie). [qualities] is empty for a file that can't be converted. */
@JsonClass(generateAdapter = true)
data class DownloadOptions(
    val file_id: String = "",
    val qualities: List<DownloadQuality> = emptyList(),
    /** The bitrate cap this user is held to here; null / 0 = none. */
    val bitrate_cap_kbps: Int? = null,
)

/** One entry of the download menu ("720p · 4 Mbps"). [allowed] is false when
 *  the user's streaming limit is below it. */
@JsonClass(generateAdapter = true)
data class DownloadQuality(
    val id: String,
    val label: String = id,
    val bitrate_kbps: Int = 0,
    val max_height: Int = 0,
    val allowed: Boolean = true,
)

/** Body for POST /items/{id}/downloads. */
@JsonClass(generateAdapter = true)
data class CreateDownloadJobRequest(
    val quality: String,
    val file_id: String? = null,
)

/** One converted download (POST /items/{id}/downloads, GET /downloads/{job}). */
@JsonClass(generateAdapter = true)
data class DownloadJob(
    val id: String,
    val item_id: String = "",
    val file_id: String = "",
    val quality: String = "",
    val quality_label: String? = null,
    /** queued | running | ready | failed | expired — see [DownloadJobStatus]. */
    val status: String,
    /** The encode's progress, 0..1. */
    val progress: Double = 0.0,
    val error: String? = null,
    val size_bytes: Long? = null,
    /** Server-relative; set once [status] is ready. Range-capable. */
    val file_url: String? = null,
)

object DownloadJobStatus {
    const val QUEUED = "queued"
    const val RUNNING = "running"
    const val READY = "ready"
    const val FAILED = "failed"
    const val EXPIRED = "expired"
}
