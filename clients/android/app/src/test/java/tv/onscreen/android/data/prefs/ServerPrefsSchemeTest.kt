package tv.onscreen.android.data.prefs

import com.google.common.truth.Truth.assertThat
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
}
