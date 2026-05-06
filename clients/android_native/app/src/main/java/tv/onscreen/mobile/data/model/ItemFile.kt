package tv.onscreen.mobile.data.model

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
    // ── audiophile metadata (v2.1 music track) ─────────────────
    /** Bit depth in bits per sample. 16 / 24 / 32. Null for codecs
     *  where the concept doesn't apply (lossy AAC etc.). */
    val bit_depth: Int? = null,
    /** Sample rate in Hz. 44100 / 48000 / 96000 / 192000 are the
     *  values the badge component knows how to label. */
    val sample_rate: Int? = null,
    val channel_layout: String? = null,
    /** True for FLAC / ALAC / WAV / DSD; false for MP3 / AAC / OGG.
     *  Drives the "Lossless" chip on the item detail. */
    val lossless: Boolean? = null,
    val replaygain_track_gain: Double? = null,
    val replaygain_track_peak: Double? = null,
    val replaygain_album_gain: Double? = null,
    val replaygain_album_peak: Double? = null,
    val audio_streams: List<AudioStream> = emptyList(),
    val subtitle_streams: List<SubtitleStream> = emptyList(),
    val chapters: List<Chapter> = emptyList(),
)
