package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class SubtitleStream(
    val index: Int,
    val codec: String,
    val language: String,
    val title: String,
    val forced: Boolean,
    val sdh: Boolean = false,
    /** A picture-based track (PGS / VobSub / DVB): the server can't serve it
     *  as text, so a remux / transcode either burns it into the video or
     *  plays its OCR'd [text_version_id]. Absent from a server older than
     *  v2.6: the client then judges by [codec] (SubtitleTracks.isImageBased). */
    val image_based: Boolean? = null,
    /** The id of the files[].external_subtitles entry OCR'd from this stream
     *  (source "ocr"), once one exists: played as text instead of burning
     *  the picture track in. */
    val text_version_id: String? = null,
)
