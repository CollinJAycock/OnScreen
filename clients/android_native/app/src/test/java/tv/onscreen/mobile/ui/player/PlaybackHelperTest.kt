package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.ItemFile

class PlaybackHelperTest {

    private fun file(
        container: String? = "mp4",
        video: String? = "h264",
        audio: String? = "aac",
        height: Int? = 1080,
    ) = ItemFile(
        id = "f1",
        stream_url = "/media/files/f1",
        container = container,
        video_codec = video,
        audio_codec = audio,
        resolution_h = height,
    )

    @Test
    fun `mp4 h264 aac is direct play`() {
        assertThat(PlaybackHelper.decide(file())).isEqualTo(PlaybackMode.DirectPlay)
    }

    @Test
    fun `mkv hevc opus is direct play`() {
        val mode = PlaybackHelper.decide(file(container = "mkv", video = "hevc", audio = "opus"))
        assertThat(mode).isEqualTo(PlaybackMode.DirectPlay)
    }

    @Test
    fun `audio-only file always direct plays`() {
        // Empty video codec is the audio-only signal — flac/mp3/etc. always
        // direct play because there's no video track to negotiate around.
        val mode = PlaybackHelper.decide(file(video = null, container = "flac", audio = "flac"))
        assertThat(mode).isEqualTo(PlaybackMode.DirectPlay)
    }

    @Test
    fun `incompatible container with compatible codec triggers remux`() {
        // h264 in avi: codec is fine for direct play, but ExoPlayer can't
        // reliably range-request avi. Remux to mp4 keeps the bytes intact
        // (no re-encode) but switches the container.
        val mode = PlaybackHelper.decide(file(container = "avi", video = "h264", audio = "aac"))
        assertThat(mode).isEqualTo(PlaybackMode.Remux)
    }

    @Test
    fun `unsupported video codec triggers transcode`() {
        val mode = PlaybackHelper.decide(file(container = "avi", video = "mpeg2", audio = "mp2"))
        assertThat(mode).isInstanceOf(PlaybackMode.Transcode::class.java)
        assertThat((mode as PlaybackMode.Transcode).height).isEqualTo(1080)
    }

    @Test
    fun `4k source triggers 4k transcode height`() {
        val mode = PlaybackHelper.decide(file(container = "avi", video = "mpeg2", height = 2160))
        assertThat((mode as PlaybackMode.Transcode).height).isEqualTo(2160)
    }

    @Test
    fun `server verdicts map to modes, and no verdict to the local decision`() {
        val f = file()
        assertThat(PlaybackHelper.modeFor("directPlay", f)).isEqualTo(PlaybackMode.DirectPlay)
        assertThat(PlaybackHelper.modeFor("directStream", f)).isEqualTo(PlaybackMode.Remux)
        assertThat(PlaybackHelper.modeFor("transcode", f)).isEqualTo(PlaybackMode.Transcode(1080))
        assertThat(PlaybackHelper.modeFor("transcode", file(height = 2160))).isEqualTo(PlaybackMode.Transcode(2160))
        assertThat(PlaybackHelper.modeFor(null, file(container = "avi"))).isEqualTo(PlaybackMode.Remux)
    }

    @Test
    fun `a server transcode asks for the same height the local decision does`() {
        // The phone advertises maxHeight=2160 whatever its screen, so there
        // is no panel cap on either path.
        val k4 = file(container = "avi", video = "mpeg2", height = 2160)
        assertThat(PlaybackHelper.modeFor("transcode", k4)).isEqualTo(PlaybackHelper.decide(k4))
        assertThat(PlaybackHelper.transcodeHeight(file(height = null))).isEqualTo(1080)
        assertThat(PlaybackHelper.transcodeHeight(file(height = 4320))).isEqualTo(2160)
        assertThat(PlaybackHelper.transcodeHeight(file(height = 720))).isEqualTo(1080)
    }

    @Test
    fun `a 409 TRANSCODE_REQUIRED on the stream is the server requiring a transcode`() {
        assertThat(transcodeRequiredFromBody("""{"error":{"code":"TRANSCODE_REQUIRED","message":"x"}}""".toByteArray()))
            .isTrue()
        // A proxy that dropped the body: /media/stream answers no other 409.
        assertThat(transcodeRequiredFromBody(null)).isTrue()
        assertThat(transcodeRequiredFromBody(ByteArray(0))).isTrue()
        assertThat(transcodeRequiredFromBody("""{"error":{"code":"CONFLICT"}}""".toByteArray())).isFalse()
        assertThat(transcodeRequiredFromBody("<html>".toByteArray())).isFalse()
        assertThat(transcodeRequired(null)).isFalse()
        assertThat(transcodeRequired(java.io.IOException("reset"))).isFalse()
    }

    @Test
    fun `one part of a stacked movie never direct-plays`() {
        // Server unreachable: the local decision would direct-play cd1 alone
        // and stop where it ends. A remux session from any part's file_id
        // streams every part as one movie.
        val part = file().copy(part_index = 1)
        assertThat(PlaybackHelper.modeFor(null, part)).isEqualTo(PlaybackMode.Remux)
        assertThat(PlaybackHelper.modeFor("directPlay", part)).isEqualTo(PlaybackMode.Remux)
        // Anything that already goes through the server stays as it is.
        assertThat(PlaybackHelper.modeFor("transcode", part)).isEqualTo(PlaybackMode.Transcode(1080))
        // A file that isn't a part is untouched.
        assertThat(PlaybackHelper.forStack(PlaybackMode.DirectPlay, file())).isEqualTo(PlaybackMode.DirectPlay)
    }

    @Test
    fun `the capability header declares optimized versions`() {
        val header = PlaybackHelper.clientCapabilitiesHeader()
        assertThat(header.split(",")).contains("optimized=1")
    }

    @Test
    fun `the capability header declares no profile or level without a hardware decoder`() {
        // This JVM has no decoders: every codec is left ungated, as before
        // v2.7, rather than guessed (VideoProfileLevelTest has the mapping).
        val keys = PlaybackHelper.clientCapabilitiesHeader().split(",").map { it.substringBefore("=") }
        assertThat(keys).containsNoneOf("h264Profile", "h264Level", "hevcProfile", "hevcLevel", "av1Profile", "av1Level")
        assertThat(keys).contains("videoDecoder")
    }

    @Test
    fun `only hardware decoders count for the profile and level keys`() {
        // The TV app's isHardwareCodec cases, with phone decoder names.
        // Android 10+: the platform's own verdict.
        assertThat(PlaybackHelper.isHardwareCodec("c2.qti.hevc.decoder", true)).isTrue()
        assertThat(PlaybackHelper.isHardwareCodec("c2.android.av1.decoder", false)).isFalse()
        // Before: known software decoders by name.
        assertThat(PlaybackHelper.isHardwareCodec("OMX.qcom.video.decoder.avc", null)).isTrue()
        assertThat(PlaybackHelper.isHardwareCodec("c2.exynos.h264.decoder", null)).isTrue()
        assertThat(PlaybackHelper.isHardwareCodec("c2.android.av1.decoder", null)).isFalse()
        assertThat(PlaybackHelper.isHardwareCodec("c2.google.av1.decoder", null)).isFalse()
        assertThat(PlaybackHelper.isHardwareCodec("OMX.google.h264.decoder", null)).isFalse()
        assertThat(PlaybackHelper.isHardwareCodec("OMX.ffmpeg.av1.decoder", null)).isFalse()
        assertThat(PlaybackHelper.isHardwareCodec("OMX.SEC.avc.sw.dec", null)).isFalse()
        assertThat(PlaybackHelper.isHardwareCodec("libgav1", null)).isFalse()
        // Secure-only decoders play DRM streams alone.
        assertThat(PlaybackHelper.isHardwareCodec("OMX.qcom.video.decoder.avc.secure", true)).isFalse()
    }

    // Dolby and DTS are claimed only with a decoder for them (the JVM test
    // runtime lists none, like a Meta Quest): a Dolby film then goes to the
    // server for its audio, instead of direct-playing without sound.
    @Test
    fun `dolby and dts audio are claimed only with a decoder`() {
        val header = PlaybackHelper.clientCapabilitiesHeader()
        val audio = header.split(",").first { it.startsWith("audioDecoder=") }
        assertThat(audio).isEqualTo("audioDecoder=aac:mp3:opus:flac:vorbis")
        assertThat(PlaybackHelper.decide(file(container = "mkv", video = "h264", audio = "eac3")))
            .isEqualTo(PlaybackMode.Remux)
        assertThat(PlaybackHelper.decide(file(container = "mkv", video = "h264", audio = "dts")))
            .isEqualTo(PlaybackMode.Remux)
    }
}
