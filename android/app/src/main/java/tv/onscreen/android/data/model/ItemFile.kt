package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class ItemFile(
    val id: String,
    val stream_url: String,
    val container: String? = null,
    val video_codec: String? = null,
    val audio_codec: String? = null,
    val resolution_w: Int? = null,
    val resolution_h: Int? = null,
    val bitrate: Long? = null,
    val hdr_type: String? = null,
    val duration_ms: Long? = null,
    val faststart: Boolean = false,
    val audio_streams: List<AudioStream> = emptyList(),
    val subtitle_streams: List<SubtitleStream> = emptyList(),
    val chapters: List<Chapter> = emptyList(),
)
