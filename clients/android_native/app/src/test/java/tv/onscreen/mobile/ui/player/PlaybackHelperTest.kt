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
}
