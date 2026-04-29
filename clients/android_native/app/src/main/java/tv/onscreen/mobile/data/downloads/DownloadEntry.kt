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
    val status: String, // queued | downloading | completed | failed
    val poster_path: String? = null,
    val error: String? = null,
    val updated_at: Long = 0L,
)

/** JSON shape of `<filesDir>/downloads/manifest.json`. Versioned so a
 *  future schema change can migrate without data loss. */
@JsonClass(generateAdapter = true)
data class DownloadManifest(
    val version: Int = 1,
    val entries: List<DownloadEntry> = emptyList(),
)
