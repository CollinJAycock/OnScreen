package tv.onscreen.android.ui.playback

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.android.data.model.ItemFile

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
    fun `compatible h264 aac mp4 direct plays`() {
        val mode = PlaybackHelper.decide(file())
        assertThat(mode).isInstanceOf(PlaybackMode.DirectPlay::class.java)
    }

    @Test
    fun `hevc in mkv with eac3 direct plays`() {
        val mode = PlaybackHelper.decide(file(container = "mkv", video = "hevc", audio = "eac3"))
        assertThat(mode).isInstanceOf(PlaybackMode.DirectPlay::class.java)
    }

    @Test
    fun `vp9 webm vorbis direct plays`() {
        val mode = PlaybackHelper.decide(file(container = "webm", video = "vp9", audio = "vorbis"))
        assertThat(mode).isInstanceOf(PlaybackMode.DirectPlay::class.java)
    }

    @Test
    fun `null video codec means audio-only file - direct play`() {
        val mode = PlaybackHelper.decide(file(video = null, container = "mp3", audio = "mp3"))
        assertThat(mode).isInstanceOf(PlaybackMode.DirectPlay::class.java)
    }

    @Test
    fun `empty video codec means audio-only file - direct play`() {
        val mode = PlaybackHelper.decide(file(video = "", container = "flac", audio = "flac"))
        assertThat(mode).isInstanceOf(PlaybackMode.DirectPlay::class.java)
    }

    @Test
    fun `unknown container with compatible codec triggers remux`() {
        val mode = PlaybackHelper.decide(file(container = "ts", video = "h264", audio = "aac"))
        assertThat(mode).isInstanceOf(PlaybackMode.Remux::class.java)
    }

    @Test
    fun `unsupported audio with compatible video triggers remux`() {
        val mode = PlaybackHelper.decide(file(audio = "truehd"))
        assertThat(mode).isInstanceOf(PlaybackMode.Remux::class.java)
    }

    @Test
    fun `unsupported video falls back to transcode at 1080p`() {
        val mode = PlaybackHelper.decide(file(video = "mpeg2", height = 1080))
        assertThat(mode).isInstanceOf(PlaybackMode.Transcode::class.java)
        assertThat((mode as PlaybackMode.Transcode).height).isEqualTo(1080)
    }

    @Test
    fun `4k unsupported video transcodes at 2160p`() {
        val mode = PlaybackHelper.decide(file(video = "mpeg2", height = 2160))
        assertThat(mode).isInstanceOf(PlaybackMode.Transcode::class.java)
        assertThat((mode as PlaybackMode.Transcode).height).isEqualTo(2160)
    }

    @Test
    fun `transcode height defaults to 1080 when source height unknown`() {
        val mode = PlaybackHelper.decide(file(video = "mpeg2", height = null))
        assertThat(mode).isInstanceOf(PlaybackMode.Transcode::class.java)
        assertThat((mode as PlaybackMode.Transcode).height).isEqualTo(1080)
    }

    @Test
    fun `codec matching is case insensitive`() {
        val mode = PlaybackHelper.decide(file(container = "MP4", video = "H264", audio = "AAC"))
        assertThat(mode).isInstanceOf(PlaybackMode.DirectPlay::class.java)
    }

    @Test
    fun `null audio with valid video and container direct plays`() {
        val mode = PlaybackHelper.decide(file(audio = null))
        assertThat(mode).isInstanceOf(PlaybackMode.DirectPlay::class.java)
    }

    @Test
    fun `supportsHevc returns true on Android TV`() {
        assertThat(PlaybackHelper.supportsHevc()).isTrue()
    }
}
