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
    fun `supportsHevc degrades to false without a platform codec list (JVM) and does not crash`() {
        // supportsHevc() probes android.media.MediaCodecList, which is unavailable
        // under plain JVM unit tests (no codecs). It must swallow that and return
        // false rather than throw. Real on-device HEVC support is an instrumented concern.
        assertThat(PlaybackHelper.supportsHevc()).isFalse()
    }

    // ── contentDurationMs: the progress-report time base ─────────────────────
    //
    // A progress report pairs a position that has hlsOffsetMs ADDED with a
    // duration. If that duration comes from the player on a resumed HLS
    // session it is session-relative (content duration MINUS the offset), so
    // the two halves land in different time bases and position/duration
    // approaches 1.0 immediately. The server flips the item to watched on the
    // first 10 s heartbeat, and the launcher's Continue Watching row is
    // deleted mid-movie by the same ratio.

    @Test
    fun `prefers the item duration over the player's session-relative one`() {
        // 2 h movie resumed at 1 h: the HLS session reports only the remaining
        // hour. The item duration is authoritative and must win.
        val dur = PlaybackHelper.contentDurationMs(
            itemDurationMs = 7_200_000L,
            playerDurationMs = 3_600_000L,
            hlsOffsetMs = 3_600_000L,
        )
        assertThat(dur).isEqualTo(7_200_000L)
    }

    @Test
    fun `re-absolutises the player duration when the item duration is unknown`() {
        val dur = PlaybackHelper.contentDurationMs(
            itemDurationMs = null,
            playerDurationMs = 3_600_000L,
            hlsOffsetMs = 3_600_000L,
        )
        assertThat(dur).isEqualTo(7_200_000L)
    }

    @Test
    fun `a resumed session never reports the item as nearly finished`() {
        // The concrete regression: resume 90 minutes into a 2 h movie. The
        // player says 30 min remain and reports position 0 within its own
        // session; the heartbeat sends content position 90 min. Pairing that
        // with the player's 30 min gave a ratio of 3.0 (> the server's 0.9
        // watched threshold and > WatchNextManager's 0.9 delete threshold).
        val contentPositionMs = 5_400_000L // 90 min, as ProgressTracker reports it
        val dur = PlaybackHelper.contentDurationMs(
            itemDurationMs = null,
            playerDurationMs = 1_800_000L, // 30 min remaining in this session
            hlsOffsetMs = 5_400_000L,
        )
        assertThat(dur).isEqualTo(7_200_000L)
        assertThat(contentPositionMs.toFloat() / dur).isWithin(0.01f).of(0.75f)
        assertThat(contentPositionMs.toFloat() / dur).isLessThan(0.9f)
    }

    @Test
    fun `direct play is unaffected because the offset is zero`() {
        val dur = PlaybackHelper.contentDurationMs(
            itemDurationMs = null,
            playerDurationMs = 7_200_000L,
            hlsOffsetMs = 0L,
        )
        assertThat(dur).isEqualTo(7_200_000L)
    }

    @Test
    fun `unknown durations resolve to zero so callers skip the report`() {
        // ExoPlayer reports C.TIME_UNSET (negative) before the media is
        // prepared; 0 tells ProgressTracker / WatchNextManager to stay quiet
        // rather than publish a nonsense ratio.
        assertThat(
            PlaybackHelper.contentDurationMs(null, playerDurationMs = 0L, hlsOffsetMs = 0L),
        ).isEqualTo(0L)
        assertThat(
            PlaybackHelper.contentDurationMs(null, playerDurationMs = -9_223_372_036_854_775_807L, hlsOffsetMs = 1_000L),
        ).isEqualTo(0L)
        assertThat(
            PlaybackHelper.contentDurationMs(null, playerDurationMs = Long.MAX_VALUE, hlsOffsetMs = 0L),
        ).isEqualTo(0L)
        // A zero/absent item duration falls through to the player rather than
        // being taken literally.
        assertThat(
            PlaybackHelper.contentDurationMs(0L, playerDurationMs = 60_000L, hlsOffsetMs = 5_000L),
        ).isEqualTo(65_000L)
    }
}
