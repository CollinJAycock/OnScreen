package tv.onscreen.mobile.ui.player

import tv.onscreen.mobile.data.model.ItemFile

// Direct/remux/transcode decision matrix — same shape as the TV
// client's PlaybackHelper. ExoPlayer covers most codecs natively,
// so direct play is the common path; we only fall back to HLS for
// containers ExoPlayer can't seek/range-request reliably or codecs
// the SoC won't decode.
sealed class PlaybackMode {
    data object DirectPlay : PlaybackMode()
    data object Remux : PlaybackMode()
    data class Transcode(val height: Int) : PlaybackMode()
}

object PlaybackHelper {

    private val directPlayVideoCodecs = setOf("h264", "hevc", "h265", "vp9", "av1")
    private val directPlayAudioCodecs = setOf(
        "aac", "mp3", "opus", "flac", "vorbis", "ac3", "eac3", "dts",
    )
    private val directPlayContainers = setOf("mp4", "mkv", "matroska", "webm", "mov")
    private val remuxVideoCodecs = setOf("h264", "hevc", "h265", "vp9", "av1")

    fun decide(file: ItemFile): PlaybackMode {
        val video = file.video_codec?.lowercase()
        val audio = file.audio_codec?.lowercase()
        val container = file.container?.lowercase()

        // Audio-only files always direct-play — there's no video
        // codec to negotiate around.
        if (video.isNullOrEmpty()) return PlaybackMode.DirectPlay

        val videoOk = video in directPlayVideoCodecs
        val audioOk = audio.isNullOrEmpty() || audio in directPlayAudioCodecs
        val containerOk = container in directPlayContainers

        if (videoOk && audioOk && containerOk) return PlaybackMode.DirectPlay
        if (video in remuxVideoCodecs) return PlaybackMode.Remux

        return PlaybackMode.Transcode(transcodeHeight(file))
    }

    /** The server's verdict for a file whose content can't be played or
     *  converted (Dolby Vision profile 5). */
    const val VERDICT_UNSUPPORTED = "unsupported"

    /** The server's verdict for a file the integrity probe marked damaged:
     *  no client and no server pipeline plays it. */
    const val VERDICT_DAMAGED = "damaged"

    /** The transcode rung for [file]: 2160 for a 4K source, else 1080 (also
     *  when unknown). No panel cap, unlike the TV client: the phone
     *  advertises maxHeight=2160 whatever its screen ([clientCapabilitiesHeader]),
     *  so the server's verdict and [decide] already agree. */
    fun transcodeHeight(file: ItemFile): Int = if ((file.resolution_h ?: 1080) >= 2160) 2160 else 1080

    /**
     * The [PlaybackMode] for a server verdict on [file] — "directPlay" |
     * "directStream" | "transcode" — or this device's own [decide] when there
     * is none (server unreachable, an older server, an unknown verdict).
     * "unsupported" / "damaged" are the caller's to refuse before this
     * (PlayerViewModel.prepare); what this returns for them is never played.
     *
     * One part of a stacked movie never plays alone ([forStack]).
     */
    fun modeFor(verdict: String?, file: ItemFile): PlaybackMode {
        val mode = when (verdict) {
            "directPlay" -> PlaybackMode.DirectPlay
            "directStream" -> PlaybackMode.Remux
            "transcode" -> PlaybackMode.Transcode(transcodeHeight(file))
            else -> decide(file)
        }
        return forStack(mode, file)
    }

    /**
     * A part of a stacked movie ("cd1" + "cd2", [ItemFile.part_index]) is one
     * file of the movie: direct play would stream that part alone and stop
     * where it ends. A remux / transcode session started with any part's
     * file_id streams every part as one movie, so a direct play becomes a
     * remux — the server's own rule (transcode.ForStack); it never answers
     * directPlay for a part, so this matters when the decision came from
     * [decide].
     */
    fun forStack(mode: PlaybackMode, file: ItemFile): PlaybackMode =
        if (file.part_index != null && mode is PlaybackMode.DirectPlay) PlaybackMode.Remux else mode

    /** Decoder support probed once from MediaCodecList — the platform's own
     *  answer, rather than the "phones from the last several years all have
     *  HEVC" assumption this used to hardcode. Cheap (one enumeration) and
     *  cached, since it can't change while the process lives. */
    private val decoders: Set<String> by lazy {
        decoderInfos
            .flatMap { it.supportedTypes.asIterable() }
            .map { it.lowercase() }
            .toSet()
            // Enumeration failed — claim only the universal baseline.
            .ifEmpty { setOf("video/avc") }
    }

    /** The platform's decoders, enumerated once. Empty when enumeration
     *  fails. */
    private val decoderInfos: List<android.media.MediaCodecInfo> by lazy {
        try {
            android.media.MediaCodecList(android.media.MediaCodecList.REGULAR_CODECS)
                .codecInfos.filter { !it.isEncoder }
        } catch (_: Exception) {
            emptyList()
        }
    }

    private fun isHardware(info: android.media.MediaCodecInfo): Boolean = isHardwareCodec(
        info.name,
        if (android.os.Build.VERSION.SDK_INT >= 29) info.isHardwareAccelerated else null,
    )

    /**
     * Whether the decoder [name] runs on dedicated hardware — matches the TV
     * app's PlaybackHelper.isHardwareCodec. Android 10+ says so itself
     * ([hardwareAccelerated]); before that, the software decoders are known
     * by name: Android's own (OMX.google., c2.android., c2.google.),
     * FFmpeg's, Samsung's ".sw." ones, and anything outside the OMX. / c2.
     * namespaces (Media3's rule). Secure-only decoders don't count either:
     * they only play DRM streams.
     */
    internal fun isHardwareCodec(name: String, hardwareAccelerated: Boolean?): Boolean {
        val n = name.lowercase()
        if (n.endsWith(".secure")) return false
        if (hardwareAccelerated != null) return hardwareAccelerated
        return !(
            n.startsWith("omx.google.") ||
                n.startsWith("c2.android.") ||
                n.startsWith("c2.google.") ||
                n.startsWith("omx.ffmpeg.") ||
                (n.startsWith("omx.sec.") && n.contains(".sw.")) ||
                (!n.startsWith("omx.") && !n.startsWith("c2."))
            )
    }

    /** The header's video profile / level keys ([VideoProfileLevel]), from
     *  what this device's HARDWARE H.264 / HEVC / AV1 decoders report,
     *  probed once — as the TV app does. A codec with no hardware decoder
     *  sends none (not gated): a software decoder's profile list says
     *  nothing about what plays smoothly. */
    private val videoProfileLevelKeys: List<String> by lazy {
        VideoProfileLevel.headerKeys(
            VideoProfileLevel.MIME_TYPES.associateWith { hardwareProfileLevels(it) },
        )
    }

    /** The (CodecProfileLevel profile, level) pairs every hardware decoder
     *  of [mime] reports. */
    private fun hardwareProfileLevels(mime: String): List<Pair<Int, Int>> =
        decoderInfos.filter { isHardware(it) }.flatMap { info ->
            info.supportedTypes.filter { it.equals(mime, ignoreCase = true) }.flatMap { type ->
                try {
                    info.getCapabilitiesForType(type).profileLevels.map { it.profile to it.level }
                } catch (e: Exception) {
                    emptyList()
                }
            }
        }

    fun supportsHevc(): Boolean = decoders.contains("video/hevc")

    fun supportsAv1(): Boolean = decoders.contains("video/av01")

    private fun supportsVp9(): Boolean = decoders.contains("video/x-vnd.on2.vp9")

    /**
     * Builds the X-Client-Capabilities header from this device's decode support
     * — the declarative profile the server uses for transcode target selection
     * and (once adopted) the POST /items/{id}/playback-decision endpoint. Built
     * from the same MediaCodecList probes that back supportsHevc()/supportsAv1(),
     * so the header can't contradict decide().
     *
     * AV1 in particular used to be omitted while decide() happily direct-played
     * it — so the server, reading the header, transcoded AV1 files this device
     * can decode natively. See docs/capability-profiles.md.
     */
    fun clientCapabilitiesHeader(): String {
        val video = mutableListOf("h264")
        if (supportsVp9()) video.add("vp9")
        if (supportsHevc()) video.add("h265")
        if (supportsAv1()) video.add("av1")
        return listOf(
            "videoDecoder=" + video.joinToString(":"),
            "audioDecoder=aac:mp3:opus:flac:vorbis:ac3:eac3:dts",
            // Audio containers must be declared or the server's Decide
            // routes every plain FLAC/MP3/OGG/WAV to an audio-HLS remux
            // (decision.go falls to directStream for audio-only files whose
            // container the client omits). Same list the TV client sends.
            "protocols=mp4:mkv:webm:mov:ts:flac:mp3:ogg:wav:aac:aiff:m4a",
            "maxWidth=3840",
            "maxHeight=2160",
            "maxAudioChannels=8",
            "maxbitdepth=10",
            "hdr=1",
            // Protocol, not decode: this client plays a play decision's
            // optimized_version (a pre-made H.264/AAC MP4) by direct play,
            // falling back to source_decision if it fails (§13).
            "optimized=1",
        ).plus(
            // v2.7: the highest H.264 / HEVC / AV1 profile and level the
            // hardware decoders take; a file over them is transcoded.
            videoProfileLevelKeys,
        ).joinToString(",")
    }
}
