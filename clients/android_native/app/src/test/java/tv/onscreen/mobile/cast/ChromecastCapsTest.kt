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
            "videoDecoder=h264,audioDecoder=aac:mp3,protocols=hls," +
                "maxWidth=1920,maxHeight=1080,maxAudioChannels=2,maxbitdepth=8,hdr=0",
        )
    }

    @Test
    fun `4K profile string is the contract's`() {
        assertThat(ChromecastCaps.UHD).isEqualTo(
            "videoDecoder=h264:h265,audioDecoder=aac:mp3,protocols=hls," +
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
