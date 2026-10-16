package tv.onscreen.mobile.cast

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.cast.CastHls.Route
import tv.onscreen.mobile.cast.CastHls.SegmentFormat

/** Pure rules for casting an OnScreen HLS session ([CastHls]) and the LOAD
 *  payload it builds ([CastMediaInfo.buildHls]). */
class CastHlsTest {

    // ── route ────────────────────────────────────────────────────────────────

    @Test
    fun `a direct-castable file with a directPlay verdict casts directly`() {
        assertThat(CastHls.route("directPlay", directCastable = true, audioOnly = false)).isEqualTo(Route.Direct)
    }

    @Test
    fun `a direct-castable video the server would remux goes over HLS with video copy`() {
        assertThat(CastHls.route("directStream", directCastable = true, audioOnly = false))
            .isEqualTo(Route.Hls(videoCopy = true))
    }

    @Test
    fun `a directPlay verdict for a file the DMR can't take remuxes`() {
        // An MKV the Chromecast decodes: the container is the only problem.
        assertThat(CastHls.route("directPlay", directCastable = false, audioOnly = false))
            .isEqualTo(Route.Hls(videoCopy = true))
    }

    @Test
    fun `a transcode verdict re-encodes`() {
        assertThat(CastHls.route("transcode", directCastable = false, audioOnly = false))
            .isEqualTo(Route.Hls(videoCopy = false))
        assertThat(CastHls.route("transcode", directCastable = true, audioOnly = false))
            .isEqualTo(Route.Hls(videoCopy = false))
    }

    @Test
    fun `unsupported and damaged verdicts refuse`() {
        assertThat(CastHls.route("unsupported", directCastable = true, audioOnly = false))
            .isEqualTo(Route.Refuse(CastHls.Refusal.DOLBY_VISION))
        assertThat(CastHls.route("damaged", directCastable = true, audioOnly = true))
            .isEqualTo(Route.Refuse(CastHls.Refusal.DAMAGED))
    }

    @Test
    fun `no verdict keeps today's direct cast, else transcodes`() {
        assertThat(CastHls.route(null, directCastable = true, audioOnly = false)).isEqualTo(Route.Direct)
        assertThat(CastHls.route(null, directCastable = false, audioOnly = false))
            .isEqualTo(Route.Hls(videoCopy = false))
    }

    @Test
    fun `a castable audio file casts directly whatever the HLS-only profile makes of it`() {
        assertThat(CastHls.route("directStream", directCastable = true, audioOnly = true)).isEqualTo(Route.Direct)
        // Not castable as it is (ALAC, DSD): an audio session.
        assertThat(CastHls.route("directStream", directCastable = false, audioOnly = true))
            .isEqualTo(Route.Hls(videoCopy = true))
    }

    @Test
    fun `a non-default audio track never casts directly`() {
        assertThat(CastHls.route("directPlay", directCastable = true, audioOnly = false, audioTrackSelected = true))
            .isEqualTo(Route.Hls(videoCopy = true))
        assertThat(CastHls.route("directStream", directCastable = true, audioOnly = true, audioTrackSelected = true))
            .isEqualTo(Route.Hls(videoCopy = true))
    }

    @Test
    fun `a burn-in always re-encodes, but refusals still win`() {
        assertThat(CastHls.route("directPlay", directCastable = true, audioOnly = false, burnIn = true))
            .isEqualTo(Route.Hls(videoCopy = false))
        assertThat(CastHls.route("unsupported", directCastable = true, audioOnly = false, burnIn = true))
            .isEqualTo(Route.Refuse(CastHls.Refusal.DOLBY_VISION))
    }

    @Test
    fun `progress goes under the Chromecast's name`() {
        assertThat(CastHls.clientName("Living Room")).isEqualTo("Chromecast — Living Room")
        assertThat(CastHls.clientName("  ")).isEqualTo("Chromecast")
        assertThat(CastHls.clientName(null)).isEqualTo("Chromecast")
    }

    @Test
    fun `decision reported with progress follows the route`() {
        assertThat(CastHls.decisionOf(Route.Direct, burning = false)).isEqualTo("directPlay")
        assertThat(CastHls.decisionOf(Route.Hls(true), burning = false)).isEqualTo("remux")
        assertThat(CastHls.decisionOf(Route.Hls(true), burning = true)).isEqualTo("transcode")
        assertThat(CastHls.decisionOf(Route.Hls(false), burning = false)).isEqualTo("transcode")
    }

    // ── transcode height ─────────────────────────────────────────────────────

    @Test
    fun `the height is the source's tier capped at the profile, never auto`() {
        val hd = ChromecastCaps.Profile.HD
        val uhd = ChromecastCaps.Profile.UHD
        assertThat(CastHls.transcodeHeight(3840, 2160, hd)).isEqualTo(1080)
        assertThat(CastHls.transcodeHeight(3840, 2160, uhd)).isEqualTo(2160)
        // A scope 4K picture counts by its width.
        assertThat(CastHls.transcodeHeight(3840, 1600, uhd)).isEqualTo(2160)
        assertThat(CastHls.transcodeHeight(1920, 800, uhd)).isEqualTo(1080)
        assertThat(CastHls.transcodeHeight(1280, 720, uhd)).isEqualTo(720)
        assertThat(CastHls.transcodeHeight(640, 480, uhd)).isEqualTo(480)
        // Unknown: the profile's ceiling.
        assertThat(CastHls.transcodeHeight(null, null, hd)).isEqualTo(1080)
        assertThat(CastHls.transcodeHeight(null, null, uhd)).isEqualTo(2160)
        assertThat(CastHls.FALLBACK_HEIGHT).isEqualTo(1080)
    }

    // ── segment format ───────────────────────────────────────────────────────

    @Test
    fun `the start response's segment_format wins`() {
        assertThat(CastHls.segmentFormatOf("ts")).isEqualTo(SegmentFormat.TS)
        assertThat(CastHls.segmentFormatOf("fmp4")).isEqualTo(SegmentFormat.FMP4)
        assertThat(CastHls.segmentFormatOf("FMP4")).isEqualTo(SegmentFormat.FMP4)
        assertThat(CastHls.segmentFormatOf(null)).isNull()
        assertThat(CastHls.segmentFormatOf("webm")).isNull()
    }

    @Test
    fun `ts segments infer MPEG-TS`() {
        val playlist = """
            #EXTM3U
            #EXT-X-VERSION:3
            #EXT-X-TARGETDURATION:4
            #EXTINF:4.000000,
            /api/v1/transcode/sessions/s1/seg/seg00000.ts?token=abc
        """.trimIndent()
        assertThat(CastHls.inferSegmentFormat(playlist)).isEqualTo(SegmentFormat.TS)
    }

    @Test
    fun `m4s segments or an init map infer fMP4`() {
        val withMap = """
            #EXTM3U
            #EXT-X-MAP:URI="/api/v1/transcode/sessions/s1/seg/init.mp4?token=abc"
            #EXTINF:4.000000,
            /api/v1/transcode/sessions/s1/seg/seg00000.m4s?token=abc
        """.trimIndent()
        assertThat(CastHls.inferSegmentFormat(withMap)).isEqualTo(SegmentFormat.FMP4)
        val bare = "#EXTM3U\n#EXTINF:4.0,\nseg00000.m4s\n"
        assertThat(CastHls.inferSegmentFormat(bare)).isEqualTo(SegmentFormat.FMP4)
    }

    @Test
    fun `a playlist with no segment yet infers nothing`() {
        assertThat(CastHls.inferSegmentFormat("#EXTM3U\n#EXT-X-TARGETDURATION:4\n")).isNull()
    }

    @Test
    fun `a master playlist's first variant is found and resolved`() {
        val master = """
            #EXTM3U
            #EXT-X-STREAM-INF:BANDWIDTH=5000000,RESOLUTION=1920x1080
            rung0/playlist.m3u8?token=t
            #EXT-X-STREAM-INF:BANDWIDTH=2000000,RESOLUTION=1280x720
            rung1/playlist.m3u8?token=t
        """.trimIndent()
        val variant = CastHls.firstVariant(master)
        assertThat(variant).isEqualTo("rung0/playlist.m3u8?token=t")
        assertThat(CastHls.resolve("https://o.example/api/v1/transcode/static/f/master.m3u8?token=t", variant!!))
            .isEqualTo("https://o.example/api/v1/transcode/static/f/rung0/playlist.m3u8?token=t")
        assertThat(CastHls.firstVariant("#EXTM3U\n#EXTINF:4.0,\nseg.ts\n")).isNull()
    }

    @Test
    fun `segment format guess follows the server's packaging`() {
        val hd = ChromecastCaps.Profile.HD
        val uhd = ChromecastCaps.Profile.UHD
        assertThat(CastHls.guessSegmentFormat(true, "h264", 0, uhd)).isEqualTo(SegmentFormat.TS)
        assertThat(CastHls.guessSegmentFormat(true, "hevc", 0, uhd)).isEqualTo(SegmentFormat.FMP4)
        assertThat(CastHls.guessSegmentFormat(false, "hevc", 1080, hd)).isEqualTo(SegmentFormat.TS)
        assertThat(CastHls.guessSegmentFormat(false, "hevc", 2160, uhd)).isEqualTo(SegmentFormat.FMP4)
        assertThat(CastHls.guessSegmentFormat(false, "h264", 1080, uhd)).isEqualTo(SegmentFormat.TS)
        assertThat(CastHls.guessSegmentFormat(false, null, 0, uhd)).isEqualTo(SegmentFormat.TS)
    }

    // ── URLs ─────────────────────────────────────────────────────────────────

    @Test
    fun `a server-relative playlist url is made absolute on the origin`() {
        assertThat(CastHls.absolute("https://o.example/", "/api/v1/transcode/sessions/s/playlist.m3u8?token=t"))
            .isEqualTo("https://o.example/api/v1/transcode/sessions/s/playlist.m3u8?token=t")
        assertThat(CastHls.absolute("https://o.example", "api/x.m3u8"))
            .isEqualTo("https://o.example/api/x.m3u8")
        assertThat(CastHls.absolute("https://o.example", "https://cdn.example/x.m3u8"))
            .isEqualTo("https://cdn.example/x.m3u8")
    }

    // ── timeline ─────────────────────────────────────────────────────────────

    @Test
    fun `the session offset is where it really opens`() {
        // A remux opens on the keyframe before the request.
        assertThat(CastHls.sessionOffsetMs(2_712.5, 2_715_000)).isEqualTo(2_712_500)
        // A pre-encoded ladder covers the whole file.
        assertThat(CastHls.sessionOffsetMs(0.0, 2_715_000)).isEqualTo(0)
        // An older server sends no field: the request.
        assertThat(CastHls.sessionOffsetMs(null, 2_715_000)).isEqualTo(2_715_000)
    }

    @Test
    fun `the receiver starts at the request within the session`() {
        assertThat(CastHls.receiverStartMs(2_715_000, 2_712_500)).isEqualTo(2_500)
        assertThat(CastHls.receiverStartMs(2_715_000, 0)).isEqualTo(2_715_000)
        assertThat(CastHls.receiverStartMs(1_000, 5_000)).isEqualTo(0)
    }

    @Test
    fun `the receiver starts past segment 0's audio gap`() {
        assertThat(CastHls.receiverStartMs(600_000, 600_000, 1.5)).isEqualTo(1_500)
        assertThat(CastHls.receiverStartMs(605_000, 600_000, 1.5)).isEqualTo(5_000)
        assertThat(CastHls.receiverStartMs(600_000, 600_000, null)).isEqualTo(0)
        assertThat(CastHls.receiverStartMs(600_000, 600_000, 0.0)).isEqualTo(0)
    }

    @Test
    fun `content time is offset plus receiver time`() {
        assertThat(CastHls.contentMs(600_000, 12_345)).isEqualTo(612_345)
        assertThat(CastHls.contentMs(600_000, -1)).isEqualTo(600_000)
    }

    @Test
    fun `a seek within what the session produced seeks the receiver`() {
        // Session opened at 10:00, receiver at 2:00 into it (content 12:00).
        assertThat(CastHls.planSeek(11 * 60_000L, 600_000, 120_000, null))
            .isEqualTo(CastHls.Seek.Receiver(60_000))
        // Ahead, but inside the receiver's seekable range.
        assertThat(CastHls.planSeek(14 * 60_000L, 600_000, 120_000, 300_000))
            .isEqualTo(CastHls.Seek.Receiver(240_000))
    }

    @Test
    fun `a seek before the session or past what it produced starts a new one`() {
        assertThat(CastHls.planSeek(5 * 60_000L, 600_000, 120_000, 300_000))
            .isEqualTo(CastHls.Seek.NewSession(300_000))
        assertThat(CastHls.planSeek(60 * 60_000L, 600_000, 120_000, 300_000))
            .isEqualTo(CastHls.Seek.NewSession(3_600_000))
        assertThat(CastHls.planSeek(-5, 0, 0, null)).isEqualTo(CastHls.Seek.Receiver(0))
    }

    // ── subtitles ────────────────────────────────────────────────────────────

    @Test
    fun `an embedded text subtitle takes the file's stream token`() {
        assertThat(CastHls.subtitleUrl("https://o.example/", "f 1", "sub:emb:3", "stream tok", 0))
            .isEqualTo("https://o.example/media/subtitles/f%201/3?token=stream%20tok")
    }

    @Test
    fun `an attached subtitle goes through its file's route`() {
        assertThat(CastHls.subtitleUrl("https://o.example", "f", "sub:ext:9a", "tok", 0))
            .isEqualTo("https://o.example/media/subtitles/f/external/9a?token=tok")
    }

    @Test
    fun `a resumed session's cues are rebased with start_ms`() {
        assertThat(CastHls.subtitleUrl("https://o.example", "f", "sub:emb:3", "tok", 2_712_500))
            .isEqualTo("https://o.example/media/subtitles/f/3?token=tok&start_ms=2712500")
        assertThat(CastHls.subtitleUrl("https://o.example", "f", "sub:ext:9", "tok", 60_000))
            .isEqualTo("https://o.example/media/subtitles/f/external/9?token=tok&start_ms=60000")
    }

    @Test
    fun `no stream token or an unknown id gives no subtitle`() {
        assertThat(CastHls.subtitleUrl("https://o.example", "f", "sub:emb:3", null, 0)).isNull()
        assertThat(CastHls.subtitleUrl("https://o.example", "f", "sub:emb:3", "", 0)).isNull()
        assertThat(CastHls.subtitleUrl("https://o.example", "f", "nope", "tok", 0)).isNull()
        assertThat(CastHls.subtitleUrl("https://o.example", "f", "sub:emb:x", "tok", 0)).isNull()
    }

    // ── LOAD payload ─────────────────────────────────────────────────────────

    private val item = CastMediaInfo.Item(
        id = "item-1",
        type = "movie",
        title = "The Movie",
        posterPath = "Movies/The Movie (2020)/poster.jpg",
    )

    @Test
    fun `an HLS LOAD carries the playlist, MPEG-URL type and segment format`() {
        val p = CastMediaInfo.buildHls(
            item = item,
            fileId = "file-1",
            playlistUrl = "https://o.example/api/v1/transcode/sessions/s1/playlist.m3u8?token=t",
            segmentFormat = SegmentFormat.FMP4,
            hasVideo = true,
            contentDurationMs = 7_200_000,
            offsetMs = 600_000,
            receiverStartMs = 2_500,
            sessionId = "s1",
            baseUrl = "https://o.example/",
        )
        assertThat(p.contentId).isEqualTo("https://o.example/api/v1/transcode/sessions/s1/playlist.m3u8?token=t")
        assertThat(p.contentType).isEqualTo("application/x-mpegurl")
        assertThat(p.streamType).isEqualTo("BUFFERED")
        assertThat(p.hlsSegmentFormat).isEqualTo(SegmentFormat.FMP4)
        assertThat(p.hasVideo).isTrue()
        // What remains of the content after the session's start.
        assertThat(p.durationSeconds).isEqualTo(6_600)
        assertThat(p.positionSeconds).isEqualTo(2.5)
        assertThat(p.metadataType).isEqualTo(CastMediaInfo.METADATA_TYPE_MOVIE)
        assertThat(p.imageUrls)
            .containsExactly("https://o.example/artwork/Movies/The%20Movie%20%282020%29/poster.jpg?w=500")
        assertThat(p.customData).containsExactly(
            "itemId", "item-1", "fileId", "file-1", "offsetMs", "600000", "sessionId", "s1",
        )
        assertThat(p.textTracks).isEmpty()
    }

    @Test
    fun `an HLS LOAD carries the text track when there is one`() {
        val track = CastHls.TextTrack(CastHls.TEXT_TRACK_ID, "https://o.example/media/subtitles/f/3?token=t", "eng", "English")
        val p = CastMediaInfo.buildHls(
            item = item, fileId = "f", playlistUrl = "https://o.example/p.m3u8",
            segmentFormat = SegmentFormat.TS, hasVideo = true, contentDurationMs = null,
            offsetMs = 0, receiverStartMs = 0, sessionId = "", baseUrl = "https://o.example",
            textTrack = track,
        )
        assertThat(p.textTracks).containsExactly(track)
        assertThat(p.durationSeconds).isNull()
        // A static ladder answers no session id.
        assertThat(p.customData).doesNotContainKey("sessionId")
    }
}
