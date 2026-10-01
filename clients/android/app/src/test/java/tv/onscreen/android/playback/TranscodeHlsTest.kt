package tv.onscreen.android.playback

import android.net.Uri
import android.os.Looper
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.Timeline
import androidx.media3.common.util.UnstableApi
import androidx.media3.common.util.Util
import androidx.media3.datasource.DataSource
import androidx.media3.exoplayer.analytics.PlayerId
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.exoplayer.hls.playlist.HlsMediaPlaylist
import androidx.media3.exoplayer.hls.playlist.HlsMultivariantPlaylist
import androidx.media3.exoplayer.hls.playlist.HlsPlaylistParser
import androidx.media3.exoplayer.hls.playlist.HlsPlaylistTracker
import androidx.media3.exoplayer.source.MaskingMediaSource
import androidx.media3.exoplayer.source.MediaSource
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import io.mockk.mockkStatic
import io.mockk.unmockkAll
import org.junit.After
import org.junit.Before
import org.junit.Test

/**
 * Where a server session starts when asked for a position before its first
 * playlist has loaded, run through Media3's own HlsMediaSource and
 * MaskingMediaSource (the wrapper the player puts round every source): a
 * start at 0 must be the stream's head, not near its live edge.
 */
@androidx.annotation.OptIn(UnstableApi::class)
class TranscodeHlsTest {

    private val url = "http://srv/api/v1/transcode/sessions/s1/playlist.m3u8"

    @Before
    fun setUp() {
        // Plain JVM: android.jar's stubs return null here, which a media
        // source can't prepare with, and a Pair with nothing in it.
        mockkStatic(Looper::class, Uri::class, android.util.Pair::class)
        every { Looper.myLooper() } returns mockk(relaxed = true)
        every { Uri.parse(any()) } returns mockk(relaxed = true)
        every { android.util.Pair.create<Any?, Any?>(any(), any()) } answers { pair(firstArg(), secondArg()) }
    }

    private fun pair(first: Any?, second: Any?): android.util.Pair<Any?, Any?> =
        android.util.Pair<Any?, Any?>(first, second).also { p ->
            for ((name, value) in listOf("first" to first, "second" to second)) {
                android.util.Pair::class.java.getField(name).apply { isAccessible = true }.set(p, value)
            }
        }

    @After
    fun tearDown() = unmockkAll()

    /** A session playlist as ffmpeg has it mid-encode: 4 s segments, no
     *  EXT-X-PROGRAM-DATE-TIME, EVENT (a remux or audio encode) or no type
     *  (a video encode), and ENDLIST only once [ended]. */
    private fun sessionPlaylist(segments: Int, event: Boolean = true, ended: Boolean = false): HlsMediaPlaylist {
        val text = buildString {
            append("#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n")
            if (event) append("#EXT-X-PLAYLIST-TYPE:EVENT\n")
            append("#EXT-X-INDEPENDENT-SEGMENTS\n")
            repeat(segments) { append("#EXTINF:4.000000,\nseg%05d.ts\n".format(it)) }
            if (ended) append("#EXT-X-ENDLIST\n")
        }
        return HlsPlaylistParser().parse(Uri.parse(url), text.byteInputStream()) as HlsMediaPlaylist
    }

    /** What the player sees after asking for [startUs] before [playlist]
     *  loaded: where the stream starts (µs), and the timeline it got, after
     *  the playlists [then] loaded too. */
    private fun start(
        item: MediaItem,
        playlist: HlsMediaPlaylist,
        startUs: Long = 0L,
        then: List<HlsMediaPlaylist> = emptyList(),
    ): Pair<Long, Timeline> {
        val tracker = mockk<HlsPlaylistTracker>(relaxed = true) {
            every { isLive } returns !playlist.hasEndTag
            every { multivariantPlaylist } returns HlsMultivariantPlaylist.EMPTY
            every { initialStartTimeUs } returns playlist.startTimeUs
        }
        val hls = HlsMediaSource.Factory(DataSource.Factory { error("no loads in this test") })
            .setPlaylistTrackerFactory { _, _, _, _, _ -> tracker }
            .createMediaSource(item)
        val masking = MaskingMediaSource(hls, /* useLazyPreparation= */ true)
        var timeline: Timeline? = null
        masking.prepareSource({ _, t -> timeline = t }, PlayerId.UNSET, mockk(relaxed = true))
        // The player's start (a seek, or setMediaSource's start position),
        // taken against the placeholder before any playlist is in.
        val period = masking.createPeriod(
            MediaSource.MediaPeriodId(masking.timeline.getUidOfPeriod(0)),
            mockk(relaxed = true),
            startUs,
        )
        hls.onPrimaryPlaylistRefreshed(playlist)
        then.forEach(hls::onPrimaryPlaylistRefreshed)
        return period.preparePositionOverrideUs to checkNotNull(timeline)
    }

    @Test
    fun `a start at 0 is the head of a live session playlist`() {
        val playlist = sessionPlaylist(segments = 10)
        assertThat(start(TranscodeHls.mediaItem(url), playlist).first).isEqualTo(0L)
        // A video encode's playlist carries no type: live all the same.
        assertThat(start(TranscodeHls.mediaItem(url), sessionPlaylist(10, event = false)).first).isEqualTo(0L)
    }

    @Test
    fun `without the item's target offset a start at 0 began near the live edge`() {
        // The bug: 40 s written, a start at 0 is taken for "the default
        // position", three target durations (12 s) from the end: 28 s in.
        val (startUs, _) = start(MediaItem.fromUri(url), sessionPlaylist(segments = 10))
        assertThat(startUs).isEqualTo(28_000_000L)
    }

    @Test
    fun `a start past 0 is kept`() {
        val playlist = sessionPlaylist(segments = 10)
        // Past seg 0's silent head, or a seek re-issue's place in the stream.
        assertThat(start(TranscodeHls.mediaItem(url), playlist, startUs = 1_500_000L).first).isEqualTo(1_500_000L)
        assertThat(start(TranscodeHls.mediaItem(url), playlist, startUs = 30_000_000L).first).isEqualTo(30_000_000L)
    }

    @Test
    fun `a later playlist's default trails its end by the first playlist's length`() {
        // HlsMediaSource keeps the target offset clamped to the first
        // playlist (40 s): with 100 s written, the default is 60 s in, where
        // a seek to the default position lands (Next on the one item). The
        // app's players take that seek to the start instead
        // (ContentTimeForwardingPlayer, SessionPlayer).
        val (_, timeline) = start(
            TranscodeHls.mediaItem(url),
            sessionPlaylist(segments = 10),
            then = listOf(sessionPlaylist(segments = 25)),
        )
        val window = timeline.getWindow(0, Timeline.Window())
        assertThat(window.isDynamic).isTrue()
        assertThat(window.defaultPositionMs).isEqualTo(60_000L)
    }

    @Test
    fun `a finished playlist starts at its head either way`() {
        val playlist = sessionPlaylist(segments = 10, ended = true)
        assertThat(start(TranscodeHls.mediaItem(url), playlist).first).isEqualTo(0L)
        assertThat(start(MediaItem.fromUri(url), playlist).first).isEqualTo(0L)
    }

    @Test
    fun `the live speed control stays out of it`() {
        // It runs only for a window with a wall clock (windowStartTimeMs,
        // from EXT-X-PROGRAM-DATE-TIME), and HlsMediaSource pins its speeds
        // to 1 for a playlist without server control: an audiobook's speed
        // stays the listener's.
        val (_, timeline) = start(TranscodeHls.mediaItem(url), sessionPlaylist(segments = 10))
        val window = timeline.getWindow(0, Timeline.Window())
        assertThat(window.isLive()).isTrue()
        assertThat(window.windowStartTimeMs).isEqualTo(C.TIME_UNSET)
        assertThat(window.liveConfiguration!!.minPlaybackSpeed).isEqualTo(1f)
        assertThat(window.liveConfiguration!!.maxPlaybackSpeed).isEqualTo(1f)
    }

    @Test
    fun `the item carries a target offset Media3 can convert`() {
        val live = TranscodeHls.mediaItem(url).liveConfiguration
        assertThat(live.targetOffsetMs).isEqualTo(TranscodeHls.HEAD_TARGET_OFFSET_MS)
        // Util.msToUs multiplies unchecked: this must not wrap negative.
        assertThat(Util.msToUs(live.targetOffsetMs)).isGreaterThan(0L)
        // Longer than any session: a century.
        assertThat(live.targetOffsetMs).isGreaterThan(100L * 365 * 24 * 60 * 60 * 1000)
    }
}
