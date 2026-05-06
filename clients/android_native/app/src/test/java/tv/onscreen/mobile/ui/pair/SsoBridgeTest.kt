package tv.onscreen.mobile.ui.pair

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class SsoBridgeTest {

    @Test
    fun `buildPairUrl assembles the expected query`() {
        val url = SsoBridge.buildPairUrl("https://server.example", "123456", "Phone")
        assertThat(url).isEqualTo(
            "https://server.example/pair?code=123456&auto=1&device_name=Phone",
        )
    }

    @Test
    fun `buildPairUrl strips trailing slashes from server URL`() {
        val one = SsoBridge.buildPairUrl("https://server.example/", "1", "P")
        val many = SsoBridge.buildPairUrl("https://server.example///", "1", "P")
        assertThat(one).isEqualTo("https://server.example/pair?code=1&auto=1&device_name=P")
        assertThat(many).isEqualTo("https://server.example/pair?code=1&auto=1&device_name=P")
    }

    @Test
    fun `buildPairUrl URL-encodes device names with spaces and unicode`() {
        // A device name like "Collin's Pixel 8" should round-trip
        // safely — no broken querystring on the receiver.
        val url = SsoBridge.buildPairUrl(
            "https://server.example", "999999", "Collin's Pixel 8",
        )
        // Apostrophe and spaces both encoded.
        assertThat(url).contains("device_name=Collin%27s+Pixel+8")
    }

    @Test
    fun `buildPairUrl omits device_name when blank`() {
        val url = SsoBridge.buildPairUrl("https://server.example", "111111", "")
        assertThat(url).isEqualTo(
            "https://server.example/pair?code=111111&auto=1",
        )
        assertThat(url).doesNotContain("device_name")
    }

    @Test
    fun `isLaunchableServerUrl accepts http and https`() {
        assertThat(SsoBridge.isLaunchableServerUrl("https://onscreen.example")).isTrue()
        assertThat(SsoBridge.isLaunchableServerUrl("http://192.168.1.10:7070")).isTrue()
    }

    @Test
    fun `isLaunchableServerUrl rejects empty or non-http URLs`() {
        assertThat(SsoBridge.isLaunchableServerUrl("")).isFalse()
        assertThat(SsoBridge.isLaunchableServerUrl("   ")).isFalse()
        // Bare host (no scheme) — Custom Tabs would silently fail
        // because Uri.parse leaves scheme=null. Reject up-front.
        assertThat(SsoBridge.isLaunchableServerUrl("server.example")).isFalse()
        // file:// is not a real auth surface.
        assertThat(SsoBridge.isLaunchableServerUrl("file:///etc/hosts")).isFalse()
        // javascript: would fire script in the user's default
        // browser — never accept.
        assertThat(SsoBridge.isLaunchableServerUrl("javascript:alert(1)")).isFalse()
    }

    @Test
    fun `isLaunchableServerUrl is scheme-case-insensitive`() {
        assertThat(SsoBridge.isLaunchableServerUrl("HTTPS://onscreen.example")).isTrue()
        assertThat(SsoBridge.isLaunchableServerUrl("Http://onscreen.example")).isTrue()
    }
}
