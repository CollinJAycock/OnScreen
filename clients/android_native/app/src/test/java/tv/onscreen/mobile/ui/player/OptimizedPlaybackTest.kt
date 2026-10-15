package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.ItemFile
import tv.onscreen.mobile.data.model.OptimizedVersion
import tv.onscreen.mobile.data.model.PlaybackDecision

class OptimizedPlaybackTest {

    private val version = OptimizedVersion(
        id = "ov1", file_id = "f1", profile = "1080p", label = "1080p",
        stream_url = "/media/optimized/ov1", stream_token = "ov-tok",
    )

    private fun decision(
        decision: String = "directPlay",
        reason: String? = OptimizedPlayback.REASON,
        version: OptimizedVersion? = this.version,
        source: String? = "transcode",
    ) = PlaybackDecision(
        decision = decision, file_id = "f1", reason = reason,
        optimized_version = version, source_decision = source,
    )

    private val hevc = ItemFile(
        id = "f1", stream_url = "/media/stream/f1", container = "mkv",
        video_codec = "hevc", audio_codec = "truehd", resolution_h = 2160,
    )

    @Test
    fun `an optimized_version verdict plays the version`() {
        assertThat(OptimizedPlayback.versionToPlay(decision())).isEqualTo(version)
    }

    @Test
    fun `any other verdict plays the source`() {
        assertThat(OptimizedPlayback.versionToPlay(null)).isNull()
        assertThat(OptimizedPlayback.versionToPlay(decision(reason = null))).isNull()
        assertThat(OptimizedPlayback.versionToPlay(decision(reason = "quality_profile", decision = "transcode"))).isNull()
        // The reason without a version (or with a blank url) is nothing to play.
        assertThat(OptimizedPlayback.versionToPlay(decision(version = null))).isNull()
        assertThat(OptimizedPlayback.versionToPlay(decision(version = version.copy(stream_url = "")))).isNull()
        // Only ever a direct play.
        assertThat(OptimizedPlayback.versionToPlay(decision(decision = "transcode"))).isNull()
    }

    @Test
    fun `a failed version falls back to the source's own verdict`() {
        assertThat(OptimizedPlayback.fallbackMode(decision(source = "transcode"), hevc))
            .isEqualTo(PlaybackMode.Transcode(2160))
        assertThat(OptimizedPlayback.fallbackMode(decision(source = "directStream"), hevc))
            .isEqualTo(PlaybackMode.Remux)
        // A stacked movie's part still never plays alone.
        assertThat(OptimizedPlayback.fallbackMode(decision(source = "directPlay"), hevc.copy(part_index = 1)))
            .isEqualTo(PlaybackMode.Remux)
    }

    @Test
    fun `a source the server refuses has no fallback`() {
        assertThat(OptimizedPlayback.sourceUnplayable(decision(source = "unsupported"))).isTrue()
        assertThat(OptimizedPlayback.sourceUnplayable(decision(source = "damaged"))).isTrue()
        assertThat(OptimizedPlayback.sourceUnplayable(decision(source = "transcode"))).isFalse()
    }
}
