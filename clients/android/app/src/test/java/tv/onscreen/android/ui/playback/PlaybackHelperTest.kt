package tv.onscreen.android.ui.playback

import android.media.MediaCodecInfo.CodecProfileLevel
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
    fun `a server session's player duration never beats the listed one`() {
        // 2 h movie resumed at 1 h: the HLS session reports only the remaining
        // hour. The listed length is the file's and must win.
        val dur = PlaybackHelper.contentDurationMs(
            listedDurationMs = 7_200_000L,
            playerDurationMs = 3_600_000L,
            hlsOffsetMs = 3_600_000L,
            hlsSession = true,
            playerWindowDynamic = false,
        )
        assertThat(dur).isEqualTo(7_200_000L)
    }

    @Test
    fun `re-absolutises a settled session's duration when nothing is listed`() {
        val dur = PlaybackHelper.contentDurationMs(
            listedDurationMs = null,
            playerDurationMs = 3_600_000L,
            hlsOffsetMs = 3_600_000L,
            hlsSession = true,
            playerWindowDynamic = false,
        )
        assertThat(dur).isEqualTo(7_200_000L)
    }

    @Test
    fun `a growing HLS playlist's duration is never the item's`() {
        // A remux resumed at 1 h: the server's EVENT playlist so far holds four
        // minutes. Taken as the length, the first heartbeat at 1 h + a few s
        // reported the film done (the phone client did exactly this).
        assertThat(
            PlaybackHelper.contentDurationMs(
                listedDurationMs = null,
                playerDurationMs = 240_000L,
                hlsOffsetMs = 3_600_000L,
                hlsSession = true,
                playerWindowDynamic = true,
            ),
        ).isEqualTo(0L)
        // The listed length still wins while the playlist grows.
        assertThat(
            PlaybackHelper.contentDurationMs(
                listedDurationMs = 7_200_000L,
                playerDurationMs = 240_000L,
                hlsOffsetMs = 3_600_000L,
                hlsSession = true,
                playerWindowDynamic = true,
            ),
        ).isEqualTo(7_200_000L)
    }

    @Test
    fun `a settled direct play's own duration comes first`() {
        // The position is time in the file: the file's length as the player
        // reads it beats a listed runtime a minute short (TMDB's 119 min).
        assertThat(
            PlaybackHelper.contentDurationMs(
                listedDurationMs = 7_140_000L,
                playerDurationMs = 7_163_000L,
                hlsOffsetMs = 0L,
                hlsSession = false,
                playerWindowDynamic = false,
            ),
        ).isEqualTo(7_163_000L)
    }

    @Test
    fun `a direct play not yet settled uses the listed length`() {
        // Not prepared yet (C.TIME_UNSET).
        assertThat(
            PlaybackHelper.contentDurationMs(
                listedDurationMs = 7_140_000L,
                playerDurationMs = -9_223_372_036_854_775_807L,
                hlsOffsetMs = 0L,
                hlsSession = false,
                playerWindowDynamic = false,
            ),
        ).isEqualTo(7_140_000L)
        // A window still growing.
        assertThat(
            PlaybackHelper.contentDurationMs(
                listedDurationMs = 7_140_000L,
                playerDurationMs = 60_000L,
                hlsOffsetMs = 0L,
                hlsSession = false,
                playerWindowDynamic = true,
            ),
        ).isEqualTo(7_140_000L)
    }

    @Test
    fun `the listed length is the file's before the item's runtime`() {
        // The file's probed length: the position reported is time in it.
        assertThat(PlaybackHelper.listedDurationMs(7_163_000L, 7_140_000L)).isEqualTo(7_163_000L)
        // Without one, the item's (TMDB minutes, or a book's own).
        assertThat(PlaybackHelper.listedDurationMs(null, 7_140_000L)).isEqualTo(7_140_000L)
        // A zero is no length.
        assertThat(PlaybackHelper.listedDurationMs(0L, 7_140_000L)).isEqualTo(7_140_000L)
        assertThat(PlaybackHelper.listedDurationMs(0L, null)).isNull()
        assertThat(PlaybackHelper.listedDurationMs(null, null)).isNull()
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
            listedDurationMs = null,
            playerDurationMs = 1_800_000L, // 30 min remaining in this session
            hlsOffsetMs = 5_400_000L,
            hlsSession = true,
            playerWindowDynamic = false,
        )
        assertThat(dur).isEqualTo(7_200_000L)
        assertThat(contentPositionMs.toFloat() / dur).isWithin(0.01f).of(0.75f)
        assertThat(contentPositionMs.toFloat() / dur).isLessThan(0.9f)
    }

    @Test
    fun `unknown durations resolve to zero so callers skip the report`() {
        // ExoPlayer reports C.TIME_UNSET (negative) before the media is
        // prepared; 0 tells ProgressTracker / WatchNextManager to stay quiet
        // rather than publish a nonsense ratio.
        for (hls in listOf(false, true)) {
            assertThat(
                PlaybackHelper.contentDurationMs(null, 0L, 0L, hlsSession = hls, playerWindowDynamic = false),
            ).isEqualTo(0L)
            assertThat(
                PlaybackHelper.contentDurationMs(
                    null,
                    playerDurationMs = -9_223_372_036_854_775_807L,
                    hlsOffsetMs = 1_000L,
                    hlsSession = hls,
                    playerWindowDynamic = false,
                ),
            ).isEqualTo(0L)
            assertThat(
                PlaybackHelper.contentDurationMs(null, Long.MAX_VALUE, 0L, hlsSession = hls, playerWindowDynamic = false),
            ).isEqualTo(0L)
        }
        // A zero listed length falls through to the player rather than being
        // taken literally.
        assertThat(
            PlaybackHelper.contentDurationMs(0L, 60_000L, 5_000L, hlsSession = true, playerWindowDynamic = false),
        ).isEqualTo(65_000L)
    }

    // ── upNextDue ───────────────────────────────────────────────────────────

    @Test
    fun `Up Next is due in the last 25 s of the content`() {
        val lead = 25_000L
        assertThat(PlaybackHelper.upNextDue(7_174_000L, 7_200_000L, lead)).isFalse()
        assertThat(PlaybackHelper.upNextDue(7_175_000L, 7_200_000L, lead)).isTrue()
        assertThat(PlaybackHelper.upNextDue(7_200_000L, 7_200_000L, lead)).isTrue()
        // Played past the listed end: the end of the stream shows it instead.
        assertThat(PlaybackHelper.upNextDue(7_201_000L, 7_200_000L, lead)).isFalse()
    }

    @Test
    fun `Up Next is never due with no known length`() {
        // A remux resumed at 1 h whose length isn't known: its player
        // duration (the four minutes written so far) put the card up mid-film.
        assertThat(PlaybackHelper.upNextDue(3_610_000L, 0L, 25_000L)).isFalse()
        assertThat(PlaybackHelper.upNextDue(0L, 0L, 25_000L)).isFalse()
    }

    @Test
    fun `the Up Next countdown runs while playing and at the end, and holds on a pause`() {
        val ready = androidx.media3.common.Player.STATE_READY
        val ended = androidx.media3.common.Player.STATE_ENDED
        val buffering = androidx.media3.common.Player.STATE_BUFFERING
        // Playing (the card shown in the credits).
        assertThat(PlaybackHelper.upNextCountdownTicks(true, ready, true)).isTrue()
        // The end of the stream: isPlaying is false there, and the card it
        // put up sat at its first second.
        assertThat(PlaybackHelper.upNextCountdownTicks(false, ended, true)).isTrue()
        // Paused, in the credits or at the end: stay.
        assertThat(PlaybackHelper.upNextCountdownTicks(false, ready, false)).isFalse()
        assertThat(PlaybackHelper.upNextCountdownTicks(false, ended, false)).isFalse()
        // Rebuffering in the credits.
        assertThat(PlaybackHelper.upNextCountdownTicks(false, buffering, true)).isFalse()
    }

    // ── hlsStartMs ──────────────────────────────────────────────────────────

    @Test
    fun `a remux starts on the resume point, not the keyframe it opened on`() {
        // Resume at 45:00; the remux opened on the keyframe at 44:57.5.
        assertThat(PlaybackHelper.hlsStartMs(2_700_000L, 2_697_500L, 0L)).isEqualTo(2_500L)
    }

    @Test
    fun `a stream covering the whole file seeks to the resume point`() {
        // A pre-encoded ladder reports start_offset_sec 0: its 0:00 is the
        // file's, and started there a 45:00 resume played from the top.
        assertThat(PlaybackHelper.hlsStartMs(2_700_000L, 0L, 0L)).isEqualTo(2_700_000L)
    }

    @Test
    fun `a silent seg 0 head is skipped when it runs past the request`() {
        assertThat(PlaybackHelper.hlsStartMs(60_000L, 60_000L, 1_500L)).isEqualTo(1_500L)
        assertThat(PlaybackHelper.hlsStartMs(60_000L, 57_000L, 1_500L)).isEqualTo(3_000L)
    }

    @Test
    fun `a stream opening after the request starts at its head`() {
        assertThat(PlaybackHelper.hlsStartMs(10_000L, 12_000L, 0L)).isEqualTo(0L)
        assertThat(PlaybackHelper.hlsStartMs(0L, 0L, 0L)).isEqualTo(0L)
    }

    // ── isStoppedStreamStatus ───────────────────────────────────────────────

    @androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
    private fun badStatus(code: Int) =
        androidx.media3.datasource.HttpDataSource.InvalidResponseCodeException(
            code,
            "status $code",
            null,
            emptyMap(),
            androidx.media3.datasource.DataSpec(io.mockk.mockk<android.net.Uri>(relaxed = true)),
            ByteArray(0),
        )

    @androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
    private fun playerError(cause: Throwable?) = androidx.media3.common.PlaybackException(
        "boom",
        cause,
        androidx.media3.common.PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS,
    )

    @Test
    fun `a 403 or 404 load is what a server-stopped stream looks like`() {
        // A terminated session's playlist/segments 404; a stopped direct play /
        // remux is refused 403 for the stop window.
        assertThat(PlaybackHelper.isStoppedStreamStatus(playerError(badStatus(404)))).isTrue()
        assertThat(PlaybackHelper.isStoppedStreamStatus(playerError(badStatus(403)))).isTrue()
        // Found anywhere down the cause chain.
        assertThat(
            PlaybackHelper.isStoppedStreamStatus(playerError(RuntimeException("wrapped", badStatus(404)))),
        ).isTrue()
    }

    @Test
    fun `other failures are ordinary playback errors`() {
        assertThat(PlaybackHelper.isStoppedStreamStatus(playerError(badStatus(500)))).isFalse()
        assertThat(PlaybackHelper.isStoppedStreamStatus(playerError(badStatus(401)))).isFalse()
        assertThat(PlaybackHelper.isStoppedStreamStatus(playerError(java.io.IOException("reset")))).isFalse()
        assertThat(PlaybackHelper.isStoppedStreamStatus(playerError(null))).isFalse()
    }

    // ── modeFor: the server's decision, as prepare() and the background
    //    service's chain both read it ─────────────────────────────────────────

    @Test
    fun `the server's verdict picks the mode`() {
        assertThat(PlaybackHelper.modeFor("directPlay", file())).isEqualTo(PlaybackMode.DirectPlay)
        assertThat(PlaybackHelper.modeFor("directStream", file())).isEqualTo(PlaybackMode.Remux)
        assertThat(PlaybackHelper.modeFor("transcode", file(height = 1080))).isEqualTo(PlaybackMode.Transcode(1080))
        assertThat(PlaybackHelper.modeFor("transcode", file(height = 2160))).isEqualTo(PlaybackMode.Transcode(2160))
    }

    @Test
    fun `a track the server transcodes is not direct played`() {
        // The local fallback direct-plays every audio-only file; only the
        // server knows this device can't decode it.
        val dsd = file(container = "dsf", video = null, audio = "dsd_lsbf", height = null)
        assertThat(PlaybackHelper.modeFor(null, dsd)).isEqualTo(PlaybackMode.DirectPlay)
        assertThat(PlaybackHelper.modeFor("transcode", dsd)).isEqualTo(PlaybackMode.Transcode(1080))
    }

    @Test
    fun `unsupported plays nowhere, and no verdict falls back to the local decision`() {
        assertThat(PlaybackHelper.modeFor("unsupported", file())).isNull()
        assertThat(PlaybackHelper.modeFor(null, file())).isEqualTo(PlaybackHelper.decide(file()))
        assertThat(PlaybackHelper.modeFor("something new", file(container = "avi", video = "mpeg2", audio = "mp2")))
            .isEqualTo(PlaybackHelper.decide(file(container = "avi", video = "mpeg2", audio = "mp2")))
    }

    @Test
    fun `only hardware decoders count for AV1`() {
        // Android 10+: the platform's own verdict.
        assertThat(PlaybackHelper.isHardwareCodec("OMX.MTK.VIDEO.DECODER.AV1", true)).isTrue()
        assertThat(PlaybackHelper.isHardwareCodec("c2.android.av1.decoder", false)).isFalse()
        // Before: known software decoders by name.
        assertThat(PlaybackHelper.isHardwareCodec("OMX.Nvidia.h265.decode", null)).isTrue()
        assertThat(PlaybackHelper.isHardwareCodec("c2.amlogic.av1.decoder", null)).isTrue()
        assertThat(PlaybackHelper.isHardwareCodec("c2.android.av1.decoder", null)).isFalse()
        assertThat(PlaybackHelper.isHardwareCodec("OMX.google.h264.decoder", null)).isFalse()
        assertThat(PlaybackHelper.isHardwareCodec("OMX.ffmpeg.av1.decoder", null)).isFalse()
        assertThat(PlaybackHelper.isHardwareCodec("OMX.SEC.avc.sw.dec", null)).isFalse()
        assertThat(PlaybackHelper.isHardwareCodec("libgav1", null)).isFalse()
        // Secure-only decoders play DRM streams alone.
        assertThat(PlaybackHelper.isHardwareCodec("OMX.MTK.VIDEO.DECODER.AV1.secure", true)).isFalse()
    }

    @Test
    fun `DTS is claimed for a decoder or an output that takes it`() {
        assertThat(PlaybackHelper.audioDecoders(dtsDecoder = false, dtsOutput = false, truehdOutput = false)).doesNotContain("dts")
        assertThat(PlaybackHelper.audioDecoders(dtsDecoder = true, dtsOutput = false, truehdOutput = false)).contains("dts")
        // A Fire TV or Shield (no DTS decoder) on a DTS receiver.
        assertThat(PlaybackHelper.audioDecoders(dtsDecoder = false, dtsOutput = true, truehdOutput = false)).contains("dts")
        assertThat(PlaybackHelper.audioDecoders(dtsDecoder = false, dtsOutput = false, truehdOutput = false))
            .containsAtLeast("aac", "ac3", "eac3", "flac")
    }

    @Test
    fun `TrueHD is claimed only for an output that takes it`() {
        assertThat(PlaybackHelper.audioDecoders(dtsDecoder = true, dtsOutput = true, truehdOutput = false))
            .doesNotContain("truehd")
        // A Shield on an Atmos receiver: both passed through.
        assertThat(PlaybackHelper.audioDecoders(dtsDecoder = false, dtsOutput = true, truehdOutput = true))
            .containsAtLeast("dts", "truehd")
    }

    // ── decide: the local fallback reads the device as the header does ──────

    private val noPassthrough = PlaybackHelper.audioDecoders(dtsDecoder = false, dtsOutput = false, truehdOutput = false)

    private fun passesThrough(dts: Boolean = false, truehd: Boolean = false) =
        PlaybackHelper.audioDecoders(dtsDecoder = false, dtsOutput = dts, truehdOutput = truehd)

    @Test
    fun `AV1 plays as-is only with a hardware AV1 decoder`() {
        val av1 = file(container = "mkv", video = "av1", audio = "opus")
        val av1InTs = file(container = "ts", video = "av1", audio = "aac")
        // A Shield or an older stick (Android's software decoder only): a
        // server transcode, not a remux, whose copied AV1 would still decode
        // in software.
        assertThat(PlaybackHelper.decide(av1, av1 = false, audioCodecs = noPassthrough)).isEqualTo(PlaybackMode.Transcode(1080))
        assertThat(PlaybackHelper.decide(av1InTs, av1 = false, audioCodecs = noPassthrough)).isEqualTo(PlaybackMode.Transcode(1080))
        assertThat(PlaybackHelper.decide(av1, av1 = true, audioCodecs = noPassthrough)).isEqualTo(PlaybackMode.DirectPlay)
        assertThat(PlaybackHelper.decide(av1InTs, av1 = true, audioCodecs = noPassthrough)).isEqualTo(PlaybackMode.Remux)
        // This JVM has no decoders at all.
        assertThat(PlaybackHelper.decide(av1)).isEqualTo(PlaybackMode.Transcode(1080))
    }

    @Test
    fun `DTS plays as-is only where it is decoded or passed through`() {
        val dts = file(container = "mkv", video = "hevc", audio = "dts")
        // No DTS decoder and no receiver that takes it: the audio is
        // converted, the video copied. Direct play would have been silent.
        assertThat(PlaybackHelper.decide(dts, av1 = false, audioCodecs = noPassthrough)).isEqualTo(PlaybackMode.Remux)
        assertThat(PlaybackHelper.decide(dts, av1 = false, audioCodecs = passesThrough(dts = true))).isEqualTo(PlaybackMode.DirectPlay)
        val dtsDecoder = PlaybackHelper.audioDecoders(dtsDecoder = true, dtsOutput = false, truehdOutput = false)
        assertThat(PlaybackHelper.decide(dts, av1 = false, audioCodecs = dtsDecoder)).isEqualTo(PlaybackMode.DirectPlay)
        // This JVM has neither.
        assertThat(PlaybackHelper.decide(dts)).isEqualTo(PlaybackMode.Remux)
    }

    @Test
    fun `TrueHD plays as-is only where the output takes it`() {
        val truehd = file(container = "mkv", video = "hevc", audio = "truehd")
        assertThat(PlaybackHelper.decide(truehd, av1 = false, audioCodecs = noPassthrough)).isEqualTo(PlaybackMode.Remux)
        assertThat(PlaybackHelper.decide(truehd, av1 = false, audioCodecs = passesThrough(truehd = true))).isEqualTo(PlaybackMode.DirectPlay)
    }

    @Test
    fun `HDR is claimed only for a screen that shows HDR10`() {
        // Display.HdrCapabilities types: 1 Dolby Vision, 2 HDR10, 3 HLG, 4 HDR10+.
        assertThat(PlaybackHelper.displayShowsHdr10(intArrayOf(2, 3))).isTrue()
        assertThat(PlaybackHelper.displayShowsHdr10(intArrayOf(4))).isTrue()
        // An SDR screen (a 1080p TV panel) reports none.
        assertThat(PlaybackHelper.displayShowsHdr10(intArrayOf())).isFalse()
        // HLG or Dolby Vision alone isn't HDR10.
        assertThat(PlaybackHelper.displayShowsHdr10(intArrayOf(3))).isFalse()
        assertThat(PlaybackHelper.displayShowsHdr10(intArrayOf(1))).isFalse()
        // A platform that can't say keeps the old claim.
        assertThat(PlaybackHelper.displayShowsHdr10(null)).isTrue()
    }

    @Test
    fun `HLG is claimed only for a screen that lists it, and never through a SHIELD`() {
        assertThat(PlaybackHelper.displayShowsHlg(intArrayOf(2, 3, 4), "Hisense")).isTrue()
        assertThat(PlaybackHelper.displayShowsHlg(intArrayOf(3), "Amazon")).isTrue()
        // HDR10 alone, no HDR, or a platform that can't say.
        assertThat(PlaybackHelper.displayShowsHlg(intArrayOf(2, 4), "Hisense")).isFalse()
        assertThat(PlaybackHelper.displayShowsHlg(intArrayOf(), "Google")).isFalse()
        assertThat(PlaybackHelper.displayShowsHlg(null, "Google")).isFalse()
        // SHIELD sends HLG flagged as SDR whatever the TV reports.
        assertThat(PlaybackHelper.displayShowsHlg(intArrayOf(1, 2, 3), "NVIDIA")).isFalse()
        assertThat(PlaybackHelper.displayShowsHlg(intArrayOf(2, 3), "nvidia")).isFalse()
        // No maker known (a blank build property): not a SHIELD.
        assertThat(PlaybackHelper.displayShowsHlg(intArrayOf(3), null)).isTrue()
    }

    @Test
    fun `10-bit VP9 is claimed only for a Profile 2 decoder`() {
        // SHIELD's hardware VP9 decoder: Profile 0 only.
        assertThat(PlaybackHelper.decodesVp9Profile2(listOf(CodecProfileLevel.VP9Profile0))).isFalse()
        assertThat(PlaybackHelper.decodesVp9Profile2(emptyList())).isFalse()
        // 4:4:4 (Profile 1) is 8-bit too.
        assertThat(PlaybackHelper.decodesVp9Profile2(listOf(CodecProfileLevel.VP9Profile0, CodecProfileLevel.VP9Profile1)))
            .isFalse()
        assertThat(PlaybackHelper.decodesVp9Profile2(listOf(CodecProfileLevel.VP9Profile0, CodecProfileLevel.VP9Profile2)))
            .isTrue()
        assertThat(PlaybackHelper.decodesVp9Profile2(listOf(CodecProfileLevel.VP9Profile2HDR))).isTrue()
        assertThat(PlaybackHelper.decodesVp9Profile2(listOf(CodecProfileLevel.VP9Profile2HDR10Plus))).isTrue()
    }

    @Test
    fun `the depth and HDR keys answer VP9 and HLG apart`() {
        // A SHIELD on an HDR10 TV: Main10 HEVC, 8-bit-only VP9, no HLG.
        assertThat(PlaybackHelper.depthAndHdrKeys(tenBit = true, vp9Profile2 = false, hdr10 = true, hlg = false))
            .containsExactly("maxbitdepth=10", "vp9MaxBitDepth=8", "hdr=1", "hlg=0").inOrder()
        assertThat(PlaybackHelper.depthAndHdrKeys(tenBit = true, vp9Profile2 = true, hdr10 = true, hlg = true))
            .containsExactly("maxbitdepth=10", "vp9MaxBitDepth=10", "hdr=1", "hlg=1").inOrder()
        // A screen that shows HLG but not HDR10: hlg answers for itself.
        assertThat(PlaybackHelper.depthAndHdrKeys(tenBit = true, vp9Profile2 = false, hdr10 = false, hlg = true))
            .containsExactly("maxbitdepth=10", "vp9MaxBitDepth=8", "hdr=0", "hlg=1").inOrder()
        // An 8-bit decoder claims no HDR, whatever the screen shows.
        assertThat(PlaybackHelper.depthAndHdrKeys(tenBit = false, vp9Profile2 = false, hdr10 = true, hlg = true))
            .containsExactly("maxbitdepth=8", "vp9MaxBitDepth=8", "hdr=0", "hlg=0").inOrder()
    }

    @Test
    fun `the header always carries vp9MaxBitDepth and hlg`() {
        // This JVM has no decoders and never read a screen; both keys still
        // go out, since without them the server gates neither.
        assertThat(PlaybackHelper.clientCapabilitiesHeader().split(","))
            .containsAtLeast("maxbitdepth=8", "vp9MaxBitDepth=8", "hdr=0", "hlg=0")
    }

    @Test
    fun `no HDR capabilities is unknown before Android 16, forced SDR from it`() {
        assertThat(PlaybackHelper.screenHdrTypes(null, sdk = 30)).isNull()
        assertThat(PlaybackHelper.screenHdrTypes(null, sdk = 36)).isEmpty()
        assertThat(PlaybackHelper.displayShowsHdr10(PlaybackHelper.screenHdrTypes(null, sdk = 36))).isFalse()
        assertThat(PlaybackHelper.screenHdrTypes(intArrayOf(2), sdk = 36)!!.toList()).containsExactly(2)
    }
}
