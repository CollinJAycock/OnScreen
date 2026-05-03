package tv.onscreen.android.ui.playback

import tv.onscreen.android.data.model.ItemFile

/**
 * Decides the playback strategy for a given file on Android TV.
 *
 * ExoPlayer handles far more codecs natively than a browser:
 * - Video: H.264, H.265 (hardware on most devices), VP9, AV1
 * - Audio: AAC, MP3, Opus, FLAC, Vorbis, AC3, EAC3 (passthrough), DTS
 * - Containers: MP4, MKV, WebM, MOV, TS
 *
 * So direct play covers the vast majority of content.
 */
sealed class PlaybackMode {
    /** Play the raw file via HTTP range requests. */
    data object DirectPlay : PlaybackMode()

    /** Remux: copy video, transcode audio only → HLS. */
    data object Remux : PlaybackMode()

    /** Full transcode at the given resolution → HLS. */
    data class Transcode(val height: Int) : PlaybackMode()
}

object PlaybackHelper {

    private val directPlayVideoCodecs = setOf(
        "h264", "hevc", "h265", "vp9", "av1",
    )

    private val directPlayAudioCodecs = setOf(
        "aac", "mp3", "opus", "flac", "vorbis",
        "ac3", "eac3", "dts",
    )

    private val directPlayContainers = setOf(
        "mp4", "mkv", "matroska", "webm", "mov",
    )

    /** Video codecs ExoPlayer can play but that may need container remux. */
    private val remuxVideoCodecs = setOf(
        "h264", "hevc", "h265", "vp9", "av1",
    )

    fun decide(file: ItemFile): PlaybackMode {
        val video = file.video_codec?.lowercase()
        val audio = file.audio_codec?.lowercase()
        val container = file.container?.lowercase()

        // Audio-only files — always direct play.
        if (video.isNullOrEmpty()) return PlaybackMode.DirectPlay

        val videoOk = video in directPlayVideoCodecs
        val audioOk = audio.isNullOrEmpty() || audio in directPlayAudioCodecs
        val containerOk = container in directPlayContainers

        if (videoOk && audioOk && containerOk) {
            return PlaybackMode.DirectPlay
        }

        // Video codec is compatible but container or audio isn't — remux.
        if (video in remuxVideoCodecs) {
            return PlaybackMode.Remux
        }

        // Everything else needs full transcode.
        val sourceH = file.resolution_h ?: 1080
        val defaultHeight = if (sourceH >= 2160) 2160 else 1080
        return PlaybackMode.Transcode(defaultHeight)
    }

    /** Whether the device likely supports HEVC hardware decode. */
    fun supportsHevc(): Boolean {
        // Almost all Fire TV and Android TV devices from 2016+ support HEVC.
        // We tell the server we support HEVC so it can use HEVC output when
        // transcoding, saving bandwidth.
        return true
    }

    /** Whether the device likely supports AV1 hardware decode. v2.1.
     *
     * When true and the source file is AV1, the server prefers the
     * AV1 fMP4 remux path (av01 tag, .m4s segments, #EXT-X-MAP) over
     * an H.264 NVENC/QSV/AMF re-encode — same bytes off disk to the
     * client, no GPU encode work on the server.
     *
     * AV1 hardware decode landed broadly on Android TV devices from
     * 2022 onward (Fire TV Cube 3rd gen, Chromecast 4K, Shield 2024,
     * any TV with MediaTek MT9602 / Realtek RTD2843 / Amlogic S905X4
     * or newer SoC). On older boxes ExoPlayer can still software-
     * decode AV1 but the CPU cost makes 4K stutter — better to let
     * the server H.264-transcode in those cases.
     *
     * Conservative default: true. ExoPlayer's MediaCodec selection
     * will fall back to software decode if hardware is missing, and
     * the AV1 software decoder ships with libgav1; 1080p AV1 plays
     * fine on basically every Android TV box. The corner case is 4K
     * AV1 on a 2018-era Fire TV — uncommon enough that we'd rather
     * default-on and let users opt out via settings if it ever
     * matters than default-off and lose the remux fast-path. */
    fun supportsAv1(): Boolean {
        return true
    }
}
