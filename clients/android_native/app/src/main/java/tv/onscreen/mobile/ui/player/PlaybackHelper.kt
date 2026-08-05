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

        val sourceH = file.resolution_h ?: 1080
        val defaultHeight = if (sourceH >= 2160) 2160 else 1080
        return PlaybackMode.Transcode(defaultHeight)
    }

    /** Decoder support probed once from MediaCodecList — the platform's own
     *  answer, rather than the "phones from the last several years all have
     *  HEVC" assumption this used to hardcode. Cheap (one enumeration) and
     *  cached, since it can't change while the process lives. */
    private val decoders: Set<String> by lazy {
        try {
            android.media.MediaCodecList(android.media.MediaCodecList.REGULAR_CODECS)
                .codecInfos
                .filter { !it.isEncoder }
                .flatMap { it.supportedTypes.asIterable() }
                .map { it.lowercase() }
                .toSet()
        } catch (_: Exception) {
            // Enumeration failed — claim only the universal baseline.
            setOf("video/avc")
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
            "protocols=mp4:mkv:webm:mov:ts",
            "maxWidth=3840",
            "maxHeight=2160",
            "maxAudioChannels=8",
            "maxbitdepth=10",
            "hdr=1",
        ).joinToString(",")
    }
}
