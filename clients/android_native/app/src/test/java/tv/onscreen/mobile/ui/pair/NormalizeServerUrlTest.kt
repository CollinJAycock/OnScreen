package tv.onscreen.mobile.ui.pair

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class NormalizeServerUrlTest {

    @Test
    fun `keeps explicit http scheme`() {
        assertThat(normalizeServerUrl("http://onscreen.example.com")).isEqualTo("http://onscreen.example.com")
    }

    @Test
    fun `keeps explicit https scheme`() {
        assertThat(normalizeServerUrl("https://onscreen.example.com")).isEqualTo("https://onscreen.example.com")
    }

    @Test
    fun `case-insensitive scheme passes through`() {
        assertThat(normalizeServerUrl("HTTPS://onscreen.example.com")).isEqualTo("HTTPS://onscreen.example.com")
    }

    @Test
    fun `public hostname defaults to https`() {
        assertThat(normalizeServerUrl("onscreen.example.com")).isEqualTo("https://onscreen.example.com")
    }

    @Test
    fun `localhost defaults to http`() {
        assertThat(normalizeServerUrl("localhost:7070")).isEqualTo("http://localhost:7070")
    }

    @Test
    fun `rfc1918 192_168 defaults to http`() {
        assertThat(normalizeServerUrl("192.168.1.50:7070")).isEqualTo("http://192.168.1.50:7070")
    }

    @Test
    fun `rfc1918 10_x defaults to http`() {
        assertThat(normalizeServerUrl("10.0.0.5")).isEqualTo("http://10.0.0.5")
    }

    @Test
    fun `rfc1918 172_16-31 defaults to http`() {
        // Within the 172.16.0.0/12 block.
        assertThat(normalizeServerUrl("172.20.1.1")).isEqualTo("http://172.20.1.1")
        // 172.32.x is OUTSIDE the private block and should resolve to https.
        assertThat(normalizeServerUrl("172.32.1.1")).isEqualTo("https://172.32.1.1")
    }

    @Test
    fun `loopback 127 defaults to http`() {
        assertThat(normalizeServerUrl("127.0.0.1:7070")).isEqualTo("http://127.0.0.1:7070")
    }

    @Test
    fun `mDNS dot-local defaults to http`() {
        assertThat(normalizeServerUrl("homelab.local")).isEqualTo("http://homelab.local")
    }

    @Test
    fun `trimming leading and trailing whitespace`() {
        assertThat(normalizeServerUrl("  onscreen.example.com  ")).isEqualTo("https://onscreen.example.com")
    }
}
