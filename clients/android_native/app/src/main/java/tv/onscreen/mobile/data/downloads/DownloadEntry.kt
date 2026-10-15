package tv.onscreen.mobile.data.downloads

import com.squareup.moshi.JsonClass

/** One offline download tracked in the manifest. file_id is the
 *  primary key — there's exactly one downloaded copy per server-side
 *  file. The on-disk path is `<filesDir>/downloads/<file_id>.<ext>`,
 *  computed deterministically from this entry's fields. */
@JsonClass(generateAdapter = true)
data class DownloadEntry(
    val file_id: String,
    val item_id: String,
    val item_title: String,
    val item_type: String, // movie | episode | track | audiobook | …
    val container: String?, // mkv / mp4 / m4b — used to build the on-disk path
    val size_bytes: Long,
    val downloaded_bytes: Long,
    val status: String, // queued | converting | downloading | completed | failed
    val poster_path: String? = null,
    val error: String? = null,
    val updated_at: Long = 0L,
    /** A converted (smaller) copy's quality id ("720p_4mbps"), or null for
     *  the original file. The copy is an MP4 of the whole item — a stacked
     *  movie's every part — still keyed by [file_id] (its first part), so the
     *  player finds it where it looks for the original. */
    val quality: String? = null,
    /** "720p · 4 Mbps", for the Downloads list. */
    val quality_label: String? = null,
    /** The server's conversion job while it encodes ("converting"). */
    val job_id: String? = null,
    /** The encode's progress, 0..1, while "converting". */
    val convert_progress: Double = 0.0,
)

/** Manifest status values. */
object DownloadStatus {
    const val QUEUED = "queued"
    /** The server is still encoding a converted copy (see DownloadEntry.quality). */
    const val CONVERTING = "converting"
    const val DOWNLOADING = "downloading"
    const val COMPLETED = "completed"
    const val FAILED = "failed"
}

/** JSON shape of `<filesDir>/downloads/manifest.json`. Versioned so a
 *  future schema change can migrate without data loss. */
@JsonClass(generateAdapter = true)
data class DownloadManifest(
    val version: Int = 1,
    val entries: List<DownloadEntry> = emptyList(),
)
