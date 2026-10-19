package tv.onscreen.mobile.cast

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The Chromecast capability profiles are a contract shared with the web
 * sender (docs/capability-profiles.md, "Chromecast (Default Media
 * Receiver)"): these pin the exact strings so a drift fails here.
 */
class ChromecastCapsTest {

    @Test
    fun `HD profile string is the contract's`() {
        assertThat(ChromecastCaps.HD).isEqualTo(
            "videoDecoder=h264,audioDecoder=aac:mp3,protocols=mp4:hls," +
                "maxWidth=1920,maxHeight=1080,maxAudioChannels=2,maxbitdepth=8,hdr=0",
        )
    }

    @Test
    fun `4K profile string is the contract's`() {
        assertThat(ChromecastCaps.UHD).isEqualTo(
            "videoDecoder=h264:h265,audioDecoder=aac:mp3,protocols=mp4:hls," +
                "maxWidth=3840,maxHeight=2160,maxAudioChannels=6,maxbitdepth=10,hdr=1",
        )
    }

    @Test
    fun `4K-capable model names pick the 4K profile, case-insensitively`() {
        listOf(
            "Chromecast Ultra",
            "Chromecast with Google TV",
            "Google TV Streamer",
            "SHIELD Android TV",
            "BRAVIA 4K VH2",
            "Some Android TV box",
            "chromecast ULTRA",
        ).forEach { name ->
            assertThat(ChromecastCaps.forModelName(name)).isEqualTo(ChromecastCaps.Profile.UHD)
        }
    }

    @Test
    fun `other and unknown models get HD`() {
        listOf("Chromecast", "Chromecast HD", "Google Nest Hub", "", null).forEach { name ->
            assertThat(ChromecastCaps.forModelName(name)).isEqualTo(ChromecastCaps.Profile.HD)
        }
    }

    @Test
    fun `known models decide before the friendly name`() {
        // Mi Box S: a 4K box the name pattern misses.
        assertThat(ChromecastCaps.forDevice("MITV-MSSP2", "Bedroom", true)).isEqualTo(ChromecastCaps.Profile.UHD)
        // A soundbar named "Ultra" is not a Chromecast Ultra.
        assertThat(ChromecastCaps.forDevice("Bose Smart Ultra Soundbar", "Living Room", null))
            .isEqualTo(ChromecastCaps.Profile.HD)
        // A known HD model stays HD whatever it was renamed to.
        assertThat(ChromecastCaps.forDevice("Chromecast HD", "Google TV room", true))
            .isEqualTo(ChromecastCaps.Profile.HD)
    }

    @Test
    fun `an ambiguous model falls back to the friendly name`() {
        // Gen 1-3 and the 4K Chromecast with Google TV both say "Chromecast".
        assertThat(ChromecastCaps.forDevice("Chromecast", "Living Room Google TV", true))
            .isEqualTo(ChromecastCaps.Profile.UHD)
        assertThat(ChromecastCaps.forDevice("Chromecast", "Living Room TV", true))
            .isEqualTo(ChromecastCaps.Profile.HD)
        assertThat(ChromecastCaps.forDevice(null, "SHIELD", null)).isEqualTo(ChromecastCaps.Profile.UHD)
    }

    @Test
    fun `an audio-only receiver never gets 4K`() {
        assertThat(ChromecastCaps.forDevice("Google TV Streamer", "Streamer", videoOut = false))
            .isEqualTo(ChromecastCaps.Profile.HD)
        assertThat(ChromecastCaps.forDevice("Google TV Streamer", "Streamer", videoOut = true))
            .isEqualTo(ChromecastCaps.Profile.UHD)
    }

    @Test
    fun `the user's quality setting overrides detection`() {
        // A Hisense set reporting its panel model: Auto says HD, 4K forces it.
        assertThat(ChromecastCaps.forDevice("55A6H", "Living Room TV", true)).isEqualTo(ChromecastCaps.Profile.HD)
        assertThat(ChromecastCaps.forDevice("55A6H", "Living Room TV", true, ChromecastCaps.Quality.UHD))
            .isEqualTo(ChromecastCaps.Profile.UHD)
        assertThat(ChromecastCaps.forDevice("Chromecast Ultra", null, true, ChromecastCaps.Quality.HD))
            .isEqualTo(ChromecastCaps.Profile.HD)
    }

    @Test
    fun `quality round-trips its wire value, unknown is auto`() {
        ChromecastCaps.Quality.entries.forEach {
            assertThat(ChromecastCaps.Quality.fromWire(it.wire)).isEqualTo(it)
        }
        assertThat(ChromecastCaps.Quality.fromWire(null)).isEqualTo(ChromecastCaps.Quality.AUTO)
        assertThat(ChromecastCaps.Quality.fromWire("8k")).isEqualTo(ChromecastCaps.Quality.AUTO)
    }

    @Test
    fun `profiles carry their header, height and HEVC flag`() {
        assertThat(ChromecastCaps.Profile.HD.header).isEqualTo(ChromecastCaps.HD)
        assertThat(ChromecastCaps.Profile.HD.maxHeight).isEqualTo(1080)
        assertThat(ChromecastCaps.Profile.HD.hevc).isFalse()
        assertThat(ChromecastCaps.Profile.UHD.header).isEqualTo(ChromecastCaps.UHD)
        assertThat(ChromecastCaps.Profile.UHD.maxHeight).isEqualTo(2160)
        assertThat(ChromecastCaps.Profile.UHD.hevc).isTrue()
    }

    @Test
    fun `a failed 4K load retries once as HD, an HD failure is final`() {
        assertThat(ChromecastCaps.fallbackAfterFailure(ChromecastCaps.Profile.UHD)).isEqualTo(ChromecastCaps.Profile.HD)
        assertThat(ChromecastCaps.fallbackAfterFailure(ChromecastCaps.Profile.HD)).isNull()
    }
}
