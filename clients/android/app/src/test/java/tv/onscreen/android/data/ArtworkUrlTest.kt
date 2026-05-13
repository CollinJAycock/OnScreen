package tv.onscreen.android.data

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class ArtworkUrlTest {

    @Test
    fun `normaliseScheme lowercases capital Https`() {
        assertThat(normaliseScheme("Https://example.com"))
            .isEqualTo("https://example.com")
    }

    @Test
    fun `normaliseScheme lowercases all-caps HTTPS`() {
        assertThat(normaliseScheme("HTTPS://example.com"))
            .isEqualTo("https://example.com")
    }

    @Test
    fun `normaliseScheme is a pass-through for already-lowercase https`() {
        assertThat(normaliseScheme("https://example.com"))
            .isEqualTo("https://example.com")
    }

    @Test
    fun `normaliseScheme handles plain http`() {
        assertThat(normaliseScheme("Http://192.168.1.50:7070"))
            .isEqualTo("http://192.168.1.50:7070")
    }

    @Test
    fun `normaliseScheme preserves path + query case`() {
        // Only the scheme should change; everything after the :// is left alone.
        assertThat(normaliseScheme("HTTPS://Example.COM/PATH/Foo?Q=Bar"))
            .isEqualTo("https://Example.COM/PATH/Foo?Q=Bar")
    }

    @Test
    fun `normaliseScheme passes-through inputs without scheme`() {
        assertThat(normaliseScheme("example.com")).isEqualTo("example.com")
        assertThat(normaliseScheme("")).isEqualTo("")
    }

    // Full artworkUrl() coverage lives under the instrumented suite —
    // the helper calls android.net.Uri.encode which isn't available in
    // the pure-JVM unit suite. normaliseScheme is the part with logic
    // worth locking in here; the format string is trivial.
}
