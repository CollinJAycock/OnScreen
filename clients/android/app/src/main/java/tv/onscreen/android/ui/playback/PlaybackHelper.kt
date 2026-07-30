package tv.onscreen.android.ui.playback

import android.net.Uri
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

    // Device decoder inventory, probed once. The capabilities header is built from
    // this so the server only picks a transcode output (HEVC, AV1, 10-bit) the
    // device can actually decode — the old hardcoded `true`s meant a cheap/older
    // box that can't decode the server's chosen HEVC/AV1/10-bit HLS output
    // dead-ended on a hard error dialog (the HLS path has no further fallback).
    private val decoderInfos: List<android.media.MediaCodecInfo> by lazy {
        try {
            android.media.MediaCodecList(android.media.MediaCodecList.REGULAR_CODECS)
                .codecInfos.filter { !it.isEncoder }
        } catch (e: Exception) {
            emptyList()
        }
    }

    private fun hasDecoderFor(mime: String): Boolean =
        decoderInfos.any { info -> info.supportedTypes.any { it.equals(mime, ignoreCase = true) } }

    /** Whether any video decoder reports a 10-bit (Main10 / HDR10) profile. Gates
     *  the 10-bit + HDR claims so we don't request output the decoder/panel can't
     *  render (garbled or green frames, or a hard decoder error). */
    private fun supports10Bit(): Boolean {
        val tenBitProfiles = mapOf(
            "video/hevc" to setOf(
                android.media.MediaCodecInfo.CodecProfileLevel.HEVCProfileMain10,
                android.media.MediaCodecInfo.CodecProfileLevel.HEVCProfileMain10HDR10,
                android.media.MediaCodecInfo.CodecProfileLevel.HEVCProfileMain10HDR10Plus,
            ),
            "video/av01" to setOf(
                android.media.MediaCodecInfo.CodecProfileLevel.AV1ProfileMain10,
                android.media.MediaCodecInfo.CodecProfileLevel.AV1ProfileMain10HDR10,
                android.media.MediaCodecInfo.CodecProfileLevel.AV1ProfileMain10HDR10Plus,
            ),
        )
        return decoderInfos.any { info ->
            info.supportedTypes.any { type ->
                val want = tenBitProfiles[type.lowercase()] ?: return@any false
                try {
                    info.getCapabilitiesForType(type).profileLevels.any { it.profile in want }
                } catch (e: Exception) {
                    false
                }
            }
        }
    }

    /** Whether the device can decode HEVC (probed from the platform codec list). */
    fun supportsHevc(): Boolean = hasDecoderFor("video/hevc")

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
     * matters than default-off and lose the remux fast-path.
     *
     * Now probed from the platform codec list rather than hardcoded, so a box
     * with no AV1 decoder no longer advertises it and gets H.264 transcode. */
    fun supportsAv1(): Boolean = hasDecoderFor("video/av01")

    /**
     * Total duration of the ITEM in content time — the only time base a
     * progress report or completion ratio may be computed in.
     *
     * [playerDurationMs] is NOT that for a resumed HLS session: the session
     * playlist starts at [hlsOffsetMs], so the player reports only the
     * REMAINING duration. Pairing it with a position that has the offset
     * added (which both the progress heartbeat and the Watch Next publisher
     * do) puts the two halves in different time bases — a movie resumed an
     * hour in reports position ≈ duration on its first heartbeat and the
     * server marks it watched.
     *
     * Prefers the item's authoritative duration; otherwise re-absolutises the
     * player's. Returns 0 when neither is known, which callers treat as
     * "don't report".
     */
    fun contentDurationMs(itemDurationMs: Long?, playerDurationMs: Long, hlsOffsetMs: Long): Long {
        if (itemDurationMs != null && itemDurationMs > 0L) return itemDurationMs
        // C.TIME_UNSET is negative, so `<= 0` covers the unknown-duration case;
        // MAX_VALUE guards the unbounded/live shape.
        if (playerDurationMs <= 0L || playerDurationMs == Long.MAX_VALUE) return 0L
        return playerDurationMs + hlsOffsetMs
    }

    /**
     * Strips the `token` query parameter from a stream/asset URL before it
     * goes into a user-facing error dialog. The URL is shown so the user (or
     * a tunnel log) can identify which request died — but stream URLs carry
     * the per-session ?token=, and a credential on a TV screen ends up in
     * support photos. Every other query param is kept so the URL stays
     * debuggable. Non-hierarchical URIs (no query to parse) pass through.
     */
    fun sanitizeUriForDisplay(uri: Uri): String {
        if (!uri.isHierarchical || "token" !in uri.queryParameterNames) {
            return uri.toString()
        }
        val cleaned = uri.buildUpon().clearQuery()
        for (name in uri.queryParameterNames) {
            if (name == "token") continue
            uri.getQueryParameters(name).forEach { cleaned.appendQueryParameter(name, it) }
        }
        return cleaned.build().toString()
    }

    /**
     * Builds the X-Client-Capabilities header value from this device's decode
     * support — the declarative profile the server uses for transcode target
     * selection and (once adopted) the POST /items/{id}/playback-decision
     * endpoint. Built from the same supportsHevc()/supportsAv1() + codec sets
     * that decide() and the transcode request use, so it stays consistent with
     * what this client claims. ExoPlayer decodes up to 7.1, so maxAudioChannels
     * is 8 (the AAC transcode fallback still caps at 5.1 server-side). See
     * docs/capability-profiles.md for the grammar.
     */
    /** The capabilities header, built once.
     *
     *  AuthInterceptor attaches this to EVERY authenticated request, and the
     *  value is device-static — the decoder inventory behind it cannot change
     *  while the process is alive. Rebuilding it per request meant several list
     *  allocations plus a codec-profile scan on every API call. The underlying
     *  MediaCodecList probe was already cached; the assembly was not. */
    private val capabilitiesHeader: String by lazy { buildClientCapabilitiesHeader() }

    fun clientCapabilitiesHeader(): String = capabilitiesHeader

    private fun buildClientCapabilitiesHeader(): String {
        val video = mutableListOf("h264", "vp9")
        if (supportsHevc()) video.add("h265")
        if (supportsAv1()) video.add("av1")
        // DTS is probed too — claiming it unconditionally made the server pick a
        // DTS passthrough/output a DTS-less box couldn't decode.
        val audio = mutableListOf("aac", "mp3", "opus", "flac", "vorbis", "ac3", "eac3")
        if (hasDecoderFor("audio/vnd.dts")) audio.add("dts")
        val tenBit = supports10Bit()
        return listOf(
            "videoDecoder=" + video.joinToString(":"),
            "audioDecoder=" + audio.joinToString(":"),
            // Raw-audio containers must be listed too, or the server can't
            // DirectPlay a music file (e.g. a .flac track): audioDecoder=flac
            // says we decode the codec, but the play decision also checks the
            // CONTAINER, and an audio-only source in a flac/mp3/ogg/wav/aac
            // container would otherwise fall to a (broken) audio-only transcode.
            // ExoPlayer plays all of these natively, so claim them for passthrough.
            "protocols=mp4:mkv:webm:mov:ts:flac:mp3:ogg:wav:aac:aiff:m4a",
            "maxWidth=3840",
            "maxHeight=2160",
            "maxAudioChannels=8",
            // 10-bit/HDR only when a decoder actually reports a Main10/HDR profile.
            "maxbitdepth=" + if (tenBit) "10" else "8",
            "hdr=" + if (tenBit) "1" else "0",
        ).joinToString(",")
    }
}
