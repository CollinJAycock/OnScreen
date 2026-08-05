package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class ItemFile(
    val id: String,
    val stream_url: String,
    /** 24 h PASETO bound to the requesting user, scoped to /media/stream
     *  via the asset-route middleware. ExoPlayer's HTTP stack bypasses
     *  the OkHttp TokenAuthenticator, so the standard 1 h access token
     *  expires mid-stream and surfaces as ERROR_CODE_IO_BAD_HTTP_STATUS
     *  on the next range request. Prefer this when present; fall back
     *  to the access token for older server builds (omitempty on the
     *  server side leaves this null). */
    val stream_token: String? = null,
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
    /** Server-side downloaded/uploaded subtitle files (OpenSubtitles etc.) —
     *  a SEPARATE array from the container's embedded subtitle_streams.
     *  Until this field existed Moshi silently dropped it, which made the
     *  whole in-player "Download subtitles" flow inert: the download
     *  succeeded server-side, the toast said "Downloaded", and the track
     *  could never be selected. */
    val external_subtitles: List<ExternalSubtitle> = emptyList(),
    val chapters: List<Chapter> = emptyList(),
)

@JsonClass(generateAdapter = true)
data class ExternalSubtitle(
    val id: String,
    val language: String = "",
    val title: String? = null,
    val forced: Boolean = false,
    val sdh: Boolean = false,
    /** Server-relative fetch URL (`/media/external-subtitles/{id}`). */
    val url: String = "",
)
