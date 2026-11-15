package tv.onscreen.android.data.prefs

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Test

/**
 * Scheme default for a bare host typed on the 10-foot keyboard.
 *
 * This used to be an unconditional `http://`. The only upgrade path was
 * adopting an https origin when the server chose to REDIRECT, so an on-path
 * attacker answering the cleartext probe with a plain 200 pinned the origin to
 * http for the life of the install — and `POST /auth/login` then carried the
 * account password in the clear. The same thing happened with no attacker at
 * all whenever an operator's reverse proxy served content on :80 instead of
 * redirecting.
 *
 * Mirrors the phone client's NormalizeServerUrlTest; keep the two in step.
 */
class ServerPrefsSchemeTest {

    private fun scheme(input: String) = ServerPrefs.defaultSchemeFor(input)

    @Test
    fun `public hostnames default to https`() {
        assertThat(scheme("onscreen.example.com")).isEqualTo("https")
        assertThat(scheme("media.wolverscreen.com")).isEqualTo("https")
        // The deployments that made this urgent: tunnels with a public name
        // and a real cert, where TLS is the only confidentiality control.
        assertThat(scheme("foo.trycloudflare.com")).isEqualTo("https")
        assertThat(scheme("box.tail1234.ts.net")).isEqualTo("https")
    }

    @Test
    fun `public hostname keeps https when a port or path is typed too`() {
        assertThat(scheme("onscreen.example.com:7070")).isEqualTo("https")
        assertThat(scheme("onscreen.example.com/some/path")).isEqualTo("https")
        assertThat(scheme("onscreen.example.com:7070/path")).isEqualTo("https")
    }

    @Test
    fun `RFC1918 addresses stay on http`() {
        assertThat(scheme("192.168.1.50:7070")).isEqualTo("http")
        assertThat(scheme("10.0.0.5:7070")).isEqualTo("http")
        assertThat(scheme("172.16.4.2")).isEqualTo("http")
        assertThat(scheme("172.31.255.254")).isEqualTo("http")
    }

    @Test
    fun `addresses just outside RFC1918 are treated as public`() {
        // 172.15 and 172.32 are NOT private — the range is 172.16-172.31.
        assertThat(scheme("172.15.0.1")).isEqualTo("https")
        assertThat(scheme("172.32.0.1")).isEqualTo("https")
        assertThat(scheme("11.0.0.5")).isEqualTo("https")
        assertThat(scheme("192.167.1.1")).isEqualTo("https")
    }

    @Test
    fun `loopback and mDNS names stay on http`() {
        assertThat(scheme("localhost")).isEqualTo("http")
        assertThat(scheme("localhost:7070")).isEqualTo("http")
        assertThat(scheme("127.0.0.1:7070")).isEqualTo("http")
        assertThat(scheme("onscreen-server.local")).isEqualTo("http")
        assertThat(scheme("media.lan.local:7070")).isEqualTo("http")
    }

    @Test
    fun `host matching is case insensitive`() {
        assertThat(scheme("LOCALHOST")).isEqualTo("http")
        assertThat(scheme("OnScreen-Server.LOCAL")).isEqualTo("http")
        assertThat(scheme("ONSCREEN.EXAMPLE.COM")).isEqualTo("https")
    }

    @Test
    fun `LAN names, single-label names and Tailscale addresses stay on http`() {
        // These all got https (no fallback) and dead-ended in "Could not connect".
        assertThat(scheme("media.lan")).isEqualTo("http")
        assertThat(scheme("media.lan:7070/path")).isEqualTo("http")
        assertThat(scheme("nas")).isEqualTo("http")
        assertThat(scheme("plexbox:7070")).isEqualTo("http")
        assertThat(scheme("100.64.0.1")).isEqualTo("http")
        assertThat(scheme("100.101.102.103:7070")).isEqualTo("http")
        assertThat(scheme("100.127.255.254")).isEqualTo("http")
        assertThat(scheme("box.home.arpa")).isEqualTo("http")
        assertThat(scheme("onscreen.internal")).isEqualTo("http")
        assertThat(scheme("dev.localhost")).isEqualTo("http")
        assertThat(scheme("169.254.10.20")).isEqualTo("http")
    }

    @Test
    fun `just outside CGNAT and link-local is public`() {
        assertThat(scheme("100.63.255.255")).isEqualTo("https")
        assertThat(scheme("100.128.0.1")).isEqualTo("https")
        assertThat(scheme("169.253.1.1")).isEqualTo("https")
    }

    @Test
    fun `bracketed IPv6 literals are judged by their address`() {
        assertThat(scheme("[::1]:7070")).isEqualTo("http")
        assertThat(scheme("[fd12:3456::1]:7070")).isEqualTo("http")
        assertThat(scheme("[fe80::1]")).isEqualTo("http")
        assertThat(scheme("[2001:db8::1]:7070")).isEqualTo("https")
    }

    // The canonical rule (clients/webos/src/lib/api/client.ts), case by case.
    @Test
    fun `isLocalNetworkHost matches the canonical rule`() {
        val local = listOf(
            "localhost", "a.localhost", "x.local", "x.lan", "x.home.arpa", "x.internal",
            "10.1.2.3", "127.0.0.1", "172.16.0.1", "172.31.0.1", "192.168.0.1",
            "169.254.0.1", "100.64.0.1", "100.127.0.1", "::1", "[::1]", "fc00::1",
            "fd00::1", "fe80::1", "febf::1", "nas", "nas.", "x.lan.",
        )
        val remote = listOf(
            "", "example.com", "8.8.8.8", "172.32.0.1", "192.169.0.1", "100.128.0.1",
            "2001:db8::1", "fec0::1", "ff02::1", "lan.example.com",
        )
        local.forEach { assertWithMessage(it).that(ServerPrefs.isLocalNetworkHost(it)).isTrue() }
        remote.forEach { assertWithMessage(it).that(ServerPrefs.isLocalNetworkHost(it)).isFalse() }
    }
}
