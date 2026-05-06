package tv.onscreen.mobile.ui.item

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.ItemFile

class AudioQualityBadgesTest {

    private fun audio(
        codec: String? = "flac",
        container: String? = "flac",
        bd: Int? = 16,
        sr: Int? = 44_100,
        lossless: Boolean? = true,
        rgTrack: Double? = null,
        rgAlbum: Double? = null,
    ) = ItemFile(
        id = "f", stream_url = "/x", container = container,
        video_codec = null, audio_codec = codec,
        bit_depth = bd, sample_rate = sr, lossless = lossless,
        replaygain_track_gain = rgTrack, replaygain_album_gain = rgAlbum,
    )

    @Test
    fun `null file returns empty`() {
        assertThat(AudioQualityBadges.badges(null)).isEmpty()
    }

    @Test
    fun `video files get no audio badges`() {
        val movie = ItemFile(
            id = "f", stream_url = "/x", container = "mkv",
            video_codec = "h264", audio_codec = "aac", lossless = false,
            bit_depth = 16, sample_rate = 48000,
        )
        assertThat(AudioQualityBadges.badges(movie)).isEmpty()
    }

    @Test
    fun `lossy audio gets only the detail chip when bit_depth and sample_rate are present`() {
        val mp3 = audio(codec = "mp3", container = "mp3", bd = null, sr = 44_100, lossless = false)
        // No Hi-Res, no Lossless tag — just the detail chip if both
        // are present. Most lossy files don't ship bit_depth so the
        // common case here is empty; we test the rare populated case.
        val popular = audio(codec = "aac", container = "m4a", bd = 16, sr = 44_100, lossless = false)
        assertThat(AudioQualityBadges.badges(popular)).containsExactly("16/44.1")
        assertThat(AudioQualityBadges.badges(mp3)).isEmpty()
    }

    @Test
    fun `CD quality lossless gets a Lossless chip but not Hi-Res`() {
        val cd = audio(bd = 16, sr = 44_100, lossless = true)
        assertThat(AudioQualityBadges.badges(cd)).containsExactly("Lossless", "16/44.1").inOrder()
    }

    @Test
    fun `24-bit 96kHz flac is Hi-Res`() {
        val hiRes = audio(bd = 24, sr = 96_000, lossless = true)
        assertThat(AudioQualityBadges.badges(hiRes)).containsExactly("Hi-Res", "24/96").inOrder()
    }

    @Test
    fun `24-bit 192kHz dsd-output qualifies as Hi-Res`() {
        val hiRes = audio(bd = 24, sr = 192_000, lossless = true)
        assertThat(AudioQualityBadges.badges(hiRes)).containsExactly("Hi-Res", "24/192").inOrder()
    }

    @Test
    fun `24-bit 48kHz qualifies as Hi-Res via bit-depth`() {
        // The Hi-Res definition: lossless AND (bit > 16 OR sr > 48k).
        // 24/48 satisfies the bit-depth half — without the OR, this
        // would fail (sr is exactly 48k, not >48k).
        val hiRes = audio(bd = 24, sr = 48_000, lossless = true)
        assertThat(AudioQualityBadges.badges(hiRes)).contains("Hi-Res")
    }

    @Test
    fun `16-bit 96kHz lossless qualifies as Hi-Res via sample-rate`() {
        val hiRes = audio(bd = 16, sr = 96_000, lossless = true)
        assertThat(AudioQualityBadges.badges(hiRes)).contains("Hi-Res")
    }

    @Test
    fun `lossless flag false suppresses Hi-Res even if bit_sample exceeds CD`() {
        // A degraded "is_lossless: false" tag with high bit/sr is
        // contradictory but possible (mis-tagged file). Trust the
        // explicit lossless flag — without it, no Hi-Res badge.
        val odd = audio(bd = 24, sr = 96_000, lossless = false)
        assertThat(AudioQualityBadges.badges(odd)).doesNotContain("Hi-Res")
        assertThat(AudioQualityBadges.badges(odd)).doesNotContain("Lossless")
    }

    @Test
    fun `ReplayGain chip surfaces when track or album gain is present`() {
        val withTrack = audio(rgTrack = -6.5)
        val withAlbum = audio(rgAlbum = -7.2)
        val withBoth = audio(rgTrack = -6.5, rgAlbum = -7.2)
        val without = audio()
        assertThat(AudioQualityBadges.badges(withTrack)).contains("ReplayGain")
        assertThat(AudioQualityBadges.badges(withAlbum)).contains("ReplayGain")
        assertThat(AudioQualityBadges.badges(withBoth)).contains("ReplayGain")
        assertThat(AudioQualityBadges.badges(without)).doesNotContain("ReplayGain")
    }

    @Test
    fun `formatKHz keeps integers integer and others one decimal`() {
        assertThat(AudioQualityBadges.formatKHz(48_000)).isEqualTo("48")
        assertThat(AudioQualityBadges.formatKHz(96_000)).isEqualTo("96")
        assertThat(AudioQualityBadges.formatKHz(192_000)).isEqualTo("192")
        assertThat(AudioQualityBadges.formatKHz(44_100)).isEqualTo("44.1")
        assertThat(AudioQualityBadges.formatKHz(22_050)).isEqualTo("22.1")
    }

    @Test
    fun `missing bit_depth or sample_rate omits the detail chip`() {
        val onlyDepth = audio(bd = 24, sr = null, lossless = true)
        val onlyRate = audio(bd = null, sr = 96_000, lossless = true)
        // Lossless still triggers, but no `24/96` style chip.
        assertThat(AudioQualityBadges.badges(onlyDepth)).doesNotContain("24/0")
        assertThat(AudioQualityBadges.badges(onlyDepth).any { it.contains("/") }).isFalse()
        assertThat(AudioQualityBadges.badges(onlyRate).any { it.contains("/") }).isFalse()
    }
}
