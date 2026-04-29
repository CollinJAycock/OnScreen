package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/** A single OpenSubtitles search result. provider_file_id is what
 *  the download endpoint expects to fetch the actual .srt content;
 *  the rest is metadata for the picker UI so the user can tell two
 *  English-language results apart. */
@JsonClass(generateAdapter = true)
data class OnlineSubtitle(
    val provider_file_id: Int,
    val file_name: String,
    val language: String,
    val release: String? = null,
    val hearing_impaired: Boolean = false,
    val hd: Boolean = false,
    val from_trusted: Boolean = false,
    val rating: Float = 0f,
    val download_count: Int = 0,
    val uploader_name: String? = null,
)

/** POST body for /items/{id}/subtitles/download. file_id is the
 *  media_files row the subtitle gets attached to; everything else is
 *  carried over from the search result so the server doesn't have to
 *  re-query OpenSubtitles to pull the metadata it needs. */
@JsonClass(generateAdapter = true)
data class SubtitleDownloadRequest(
    val file_id: String,
    val provider_file_id: Int,
    val language: String,
    val title: String = "",
    val hearing_impaired: Boolean = false,
    val rating: Float = 0f,
    val download_count: Int = 0,
)
