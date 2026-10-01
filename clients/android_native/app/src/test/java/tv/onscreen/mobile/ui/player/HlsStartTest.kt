package tv.onscreen.mobile.ui.player

import android.net.Uri
import android.os.Looper
import android.util.Pair
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.Timeline
import androidx.media3.common.util.Util
import androidx.media3.datasource.DataSource
import androidx.media3.exoplayer.analytics.PlayerId
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.exoplayer.hls.playlist.HlsMediaPlaylist
import androidx.media3.exoplayer.hls.playlist.HlsPlaylistParser
import androidx.media3.exoplayer.hls.playlist.HlsPlaylistTracker
import androidx.media3.exoplayer.source.MaskingMediaPeriod
import androidx.media3.exoplayer.source.MaskingMediaSource
import androidx.media3.exoplayer.source.MediaSource
import androidx.media3.exoplayer.upstream.BandwidthMeter
import androidx.media3.exoplayer.upstream.DefaultAllocator
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import io.mockk.mockkStatic
import io.mockk.unmockkStatic
import org.junit.After
import org.junit.Before
import org.junit.Test

/**
 * A remux / transcode session started at the stream's 0:00 starts there
 * (see [hlsMediaItem]), against Media3's real HlsMediaSource and
 * MaskingMediaSource — and where its window's default position goes once the
 * playlist grows (see [HlsSessionPlayer]). Only the playlist loads are stood
 * in for: the tracker hands the source an EVENT playlist, parsed from the
 * text ffmpeg writes, ten 4 s segments along (or 25, later) and still growing.
 */
class HlsStartTest {

    private val uri = mockk<Uri>(relaxed = true)

    @Before
    fun androidStubs() {
        // Sources take the preparing thread's looper; the stubbed android.jar
        // has none.
        mockkStatic(Looper::class)
        every { Looper.myLooper() } returns mockk(relaxed = true)
        // Timeline hands a period and a position around in an android Pair,
        // whose stub never sets its fields.
        mockkStatic(Pair::class)
        every { Pair.create<Any, Any>(any(), any()) } answers {
            Pair<Any, Any>(null, null).also { pair ->
                for ((name, value) in listOf("first" to firstArg<Any>(), "second" to secondArg<Any>())) {
                    Pair::class.java.getField(name).apply { isAccessible = true }.set(pair, value)
                }
            }
        }
    }

    @After
    fun unmockStatics() = unmockkStatic(Looper::class, Pair::class)

    private fun eventPlaylist(segments: Int): HlsMediaPlaylist {
        val text = buildString {
            append("#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n")
            append("#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-INDEPENDENT-SEGMENTS\n")
            repeat(segments) { append("#EXTINF:4.000000,\nseg$it.ts\n") }
        }
        return HlsPlaylistParser().parse(uri, text.byteInputStream()) as HlsMediaPlaylist
    }

    private fun hlsSource(item: MediaItem): HlsMediaSource {
        val tracker = mockk<HlsPlaylistTracker>(relaxed = true)
        every { tracker.isLive } returns true
        every { tracker.initialStartTimeUs } returns 0L
        return HlsMediaSource.Factory(mockk<DataSource.Factory>(relaxed = true))
            .setPlaylistTrackerFactory { _, _, _, _, _ -> tracker }
            .createMediaSource(item)
    }

    private fun prepare(
        source: MediaSource,
        caller: MediaSource.MediaSourceCaller = MediaSource.MediaSourceCaller { _, _ -> },
    ) = source.prepareSource(caller, PlayerId.UNSET, mockk<BandwidthMeter>(relaxed = true))

    private fun prepare(source: MediaSource, onTimeline: (Timeline) -> Unit) =
        prepare(source, MediaSource.MediaSourceCaller { _, t -> onTimeline(t) })

    private fun allocator() = DefaultAllocator(true, C.DEFAULT_BUFFER_SEGMENT_SIZE)

    /** Where a period the masking source created settles once the playlist
     *  has loaded (MaskingMediaPeriod hands it to selectTracks). */
    private fun settledUs(period: MaskingMediaPeriod): Long =
        period.preparePositionOverrideUs.takeIf { it != C.TIME_UNSET } ?: period.preparePositionUs

    /** The default position of the window [item]'s source reports for the
     *  playlist. */
    private fun defaultPositionUs(item: MediaItem): Long {
        val hls = hlsSource(item)
        var timeline: Timeline? = null
        prepare(hls) { timeline = it }
        hls.onPrimaryPlaylistRefreshed(eventPlaylist(segments = 10))
        val window = timeline!!.getWindow(0, Timeline.Window())
        assertThat(window.isLive).isTrue()
        return window.defaultPositionUs
    }

    /**
     * Where the player's first period starts when playback was asked to start
     * [requestedUs] into the stream. ExoPlayer wraps every source in a
     * MaskingMediaSource and creates that period on its placeholder timeline
     * — setMediaSource, seekTo, prepare — before the playlist has loaded; the
     * position the masking source settles on once it has is where the player
     * starts.
     */
    private fun startUs(item: MediaItem, requestedUs: Long, segments: Int = 10): Long {
        val hls = hlsSource(item)
        val masking = MaskingMediaSource(hls, /* useLazyPreparation= */ true)
        prepare(masking)
        val placeholder = MediaSource.MediaPeriodId(masking.timeline.getUidOfPeriod(0))
        val period = masking.createPeriod(placeholder, allocator(), requestedUs)
        hls.onPrimaryPlaylistRefreshed(eventPlaylist(segments))
        return settledUs(period)
    }

    /** The source ExoPlayer is left to by default, as before the fix. */
    private fun plainItem() = MediaItem.Builder().setUri(uri).build()

    @Test
    fun `a live session's window defaults to its start`() {
        assertThat(defaultPositionUs(hlsMediaItem(uri))).isEqualTo(0L)
    }

    @Test
    fun `by default a live session's window defaults three target durations from its end`() {
        // 40 s along, less 3 × 4 s — what a start of 0 used to become.
        assertThat(defaultPositionUs(plainItem())).isEqualTo(28_000_000L)
    }

    @Test
    fun `a start of 0 stays at 0`() {
        assertThat(startUs(hlsMediaItem(uri), requestedUs = 0L)).isEqualTo(0L)
    }

    @Test
    fun `by default a start of 0 moved near the live edge`() {
        assertThat(startUs(plainItem(), requestedUs = 0L)).isEqualTo(28_000_000L)
    }

    @Test
    fun `a start past 0 is kept`() {
        assertThat(startUs(hlsMediaItem(uri), requestedUs = 6_500_000L)).isEqualTo(6_500_000L)
        assertThat(startUs(plainItem(), requestedUs = 6_500_000L)).isEqualTo(6_500_000L)
    }

    // ── After the first playlist load (see HlsSessionPlayer) ──────────

    @Test
    fun `once the playlist grows its default trails the end by the first playlist's length`() {
        val hls = hlsSource(hlsMediaItem(uri))
        var timeline: Timeline? = null
        prepare(hls) { timeline = it }
        hls.onPrimaryPlaylistRefreshed(eventPlaylist(segments = 10))
        hls.onPrimaryPlaylistRefreshed(eventPlaylist(segments = 25))
        // 100 s along, less the 40 s the first load clamped the offset to:
        // where Play on the ended stream and Next went.
        assertThat(timeline!!.getWindow(0, Timeline.Window()).defaultPositionUs).isEqualTo(60_000_000L)
    }

    @Test
    fun `a retry at 0 on the source that failed starts at the drifted default`() {
        val hls = hlsSource(hlsMediaItem(uri))
        val masking = MaskingMediaSource(hls, /* useLazyPreparation= */ true)
        val caller = MediaSource.MediaSourceCaller { _, _ -> }
        prepare(masking, caller)
        val placeholder = MediaSource.MediaPeriodId(masking.timeline.getUidOfPeriod(0))
        val first = masking.createPeriod(placeholder, allocator(), 0L)
        hls.onPrimaryPlaylistRefreshed(eventPlaylist(segments = 10))
        assertThat(settledUs(first)).isEqualTo(0L)

        // An error before the playlist grew: the player lets go of its period
        // and its source, and a retry at 0 prepares both again.
        masking.releasePeriod(first)
        masking.releaseSource(caller)
        prepare(masking, caller)
        val id = MediaSource.MediaPeriodId(masking.timeline.getUidOfPeriod(0))
        val retry = masking.createPeriod(id, allocator(), 0L)
        hls.onPrimaryPlaylistRefreshed(eventPlaylist(segments = 25))

        // 0 was the old window's default, so it read as no position at all.
        assertThat(settledUs(retry)).isEqualTo(60_000_000L)
    }

    @Test
    fun `a retry at 0 on a new source starts at 0`() {
        // What HlsSessionPlayer.prepare hands the player, however far the
        // server has got by then.
        assertThat(startUs(hlsMediaItem(uri), requestedUs = 0L, segments = 25)).isEqualTo(0L)
    }

    @Test
    fun `the item carries the live configuration and leaves speed control off`() {
        val live = hlsMediaItem(uri).liveConfiguration
        assertThat(live.targetOffsetMs).isEqualTo(HLS_WINDOW_START_TARGET_OFFSET_MS)
        // No speed range: HlsMediaSource pins the live speed to 1.
        assertThat(live.minPlaybackSpeed).isEqualTo(C.RATE_UNSET)
        assertThat(live.maxPlaybackSpeed).isEqualTo(C.RATE_UNSET)
    }

    @Test
    fun `the target offset outlasts any playlist without overflowing`() {
        // Media3 multiplies it into microseconds unchecked; it must come out
        // positive and past a playlist far longer than any file (100 hours).
        val offsetUs = Util.msToUs(HLS_WINDOW_START_TARGET_OFFSET_MS)
        assertThat(offsetUs).isGreaterThan(100L * 3_600_000_000L)
    }
}
