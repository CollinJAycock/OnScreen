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

    // Phones from the last several years all carry HEVC HW decode;
    // the server uses this hint to pick HEVC output during transcode
    // for half the bandwidth at equivalent quality. If we ever land on
    // a device where the hint is wrong, ExoPlayer falls back to a
    // software decoder rather than failing loud.
    fun supportsHevc(): Boolean = true
}
