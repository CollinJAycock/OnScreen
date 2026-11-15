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

    // The rest of the local-network rule (clients/webos isLocalNetworkHost).

    @Test
    fun `local suffixes default to http`() {
        assertThat(normalizeServerUrl("media.lan:7070")).isEqualTo("http://media.lan:7070")
        assertThat(normalizeServerUrl("nas.home.arpa")).isEqualTo("http://nas.home.arpa")
        assertThat(normalizeServerUrl("box.internal")).isEqualTo("http://box.internal")
        assertThat(normalizeServerUrl("dev.localhost:7070")).isEqualTo("http://dev.localhost:7070")
    }

    @Test
    fun `single-label names default to http`() {
        assertThat(normalizeServerUrl("nas:7070")).isEqualTo("http://nas:7070")
        assertThat(normalizeServerUrl("plexbox")).isEqualTo("http://plexbox")
    }

    @Test
    fun `cgnat and link-local default to http`() {
        // 100.64/10 is Tailscale's range.
        assertThat(normalizeServerUrl("100.101.102.103:7070")).isEqualTo("http://100.101.102.103:7070")
        assertThat(normalizeServerUrl("100.63.0.1")).isEqualTo("https://100.63.0.1")
        assertThat(normalizeServerUrl("100.128.0.1")).isEqualTo("https://100.128.0.1")
        assertThat(normalizeServerUrl("169.254.10.20")).isEqualTo("http://169.254.10.20")
    }

    @Test
    fun `bracketed ipv6 literals`() {
        assertThat(normalizeServerUrl("[::1]:7070")).isEqualTo("http://[::1]:7070")
        assertThat(normalizeServerUrl("[fd12:3456::1]:7070")).isEqualTo("http://[fd12:3456::1]:7070")
        assertThat(normalizeServerUrl("[fe80::1]")).isEqualTo("http://[fe80::1]")
        assertThat(normalizeServerUrl("[2001:db8::1]:7070")).isEqualTo("https://[2001:db8::1]:7070")
    }

    @Test
    fun `public addresses and names default to https`() {
        assertThat(normalizeServerUrl("8.8.8.8")).isEqualTo("https://8.8.8.8")
        assertThat(normalizeServerUrl("media.example.com/onscreen")).isEqualTo("https://media.example.com/onscreen")
        assertThat(normalizeServerUrl("onscreen.local.example.com")).isEqualTo("https://onscreen.local.example.com")
    }

    @Test
    fun `isLocalNetworkHost matches the shared rule`() {
        assertThat(isLocalNetworkHost("LOCALHOST")).isTrue()
        assertThat(isLocalNetworkHost("nas.local.")).isTrue()
        assertThat(isLocalNetworkHost("[::1]")).isTrue()
        assertThat(isLocalNetworkHost("")).isFalse()
        assertThat(isLocalNetworkHost("192.169.0.1")).isFalse()
        assertThat(isLocalNetworkHost("172.15.0.1")).isFalse()
        assertThat(isLocalNetworkHost("fc00::1")).isTrue()
        assertThat(isLocalNetworkHost("fec0::1")).isFalse()
    }
}
