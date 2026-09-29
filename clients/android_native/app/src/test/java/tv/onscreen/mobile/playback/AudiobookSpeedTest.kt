package tv.onscreen.mobile.playback

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class AudiobookSpeedTest {

    @Test
    fun `presets run from three-quarter to triple speed, slowest first`() {
        assertThat(AudiobookSpeed.PRESETS)
            .containsExactly(0.75f, 1.0f, 1.25f, 1.5f, 1.75f, 2.0f, 2.5f, 3.0f)
            .inOrder()
        // Every preset is a speed the server accepts (0.5 – 3.0).
        AudiobookSpeed.PRESETS.forEach { assertThat(AudiobookSpeed.clamp(it)).isEqualTo(it) }
    }

    @Test
    fun `clamp keeps a rate inside the server's range, to two decimals`() {
        assertThat(AudiobookSpeed.clamp(0.1)).isEqualTo(0.5f)
        assertThat(AudiobookSpeed.clamp(0.5)).isEqualTo(0.5f)
        assertThat(AudiobookSpeed.clamp(3.0)).isEqualTo(3.0f)
        assertThat(AudiobookSpeed.clamp(7.0)).isEqualTo(3.0f)
        // A float32 column read back as a double.
        assertThat(AudiobookSpeed.clamp(1.2500000476837158)).isEqualTo(1.25f)
        assertThat(AudiobookSpeed.clamp(1.333)).isEqualTo(1.33f)
    }

    @Test
    fun `clamp turns an unusable rate into normal speed`() {
        assertThat(AudiobookSpeed.clamp(Double.NaN)).isEqualTo(1.0f)
        assertThat(AudiobookSpeed.clamp(Double.POSITIVE_INFINITY)).isEqualTo(1.0f)
        assertThat(AudiobookSpeed.clamp(Float.NaN)).isEqualTo(1.0f)
    }

    @Test
    fun `label drops trailing zeros and uses a dot whatever the locale`() {
        val saved = java.util.Locale.getDefault()
        java.util.Locale.setDefault(java.util.Locale.GERMANY)
        try {
            assertThat(AudiobookSpeed.label(1.0f)).isEqualTo("1×")
            assertThat(AudiobookSpeed.label(1.5f)).isEqualTo("1.5×")
            assertThat(AudiobookSpeed.label(1.25f)).isEqualTo("1.25×")
            assertThat(AudiobookSpeed.label(0.75f)).isEqualTo("0.75×")
            assertThat(AudiobookSpeed.label(3.0f)).isEqualTo("3×")
            // Out-of-range input is labelled as what would actually play.
            assertThat(AudiobookSpeed.label(10f)).isEqualTo("3×")
        } finally {
            java.util.Locale.setDefault(saved)
        }
    }

    @Test
    fun `same compares to the server's precision`() {
        assertThat(AudiobookSpeed.same(1.5f, 1.5000001f)).isTrue()
        assertThat(AudiobookSpeed.same(1.5f, 1.25f)).isFalse()
    }

    @Test
    fun `only audiobooks and their chapters have a speed`() {
        assertThat(AudiobookSpeed.hasSpeed("audiobook")).isTrue()
        assertThat(AudiobookSpeed.hasSpeed("audiobook_chapter")).isTrue()
        // Music stays at 1×, and so does everything else.
        listOf("track", "album", "artist", "podcast", "episode", "movie", "book", null).forEach {
            assertThat(AudiobookSpeed.hasSpeed(it)).isFalse()
        }
    }

    @Test
    fun `a book's speed is its own, a chapter's is its book's`() {
        assertThat(AudiobookSpeed.bookIdOf("audiobook", "b1", "author-1")).isEqualTo("b1")
        assertThat(AudiobookSpeed.bookIdOf("audiobook_chapter", "c3", "b1")).isEqualTo("b1")
        // An orphan chapter can't be resolved (the server 404s it too).
        assertThat(AudiobookSpeed.bookIdOf("audiobook_chapter", "c3", null)).isNull()
        assertThat(AudiobookSpeed.bookIdOf("audiobook_chapter", "c3", "")).isNull()
        assertThat(AudiobookSpeed.bookIdOf("track", "t1", "album-1")).isNull()
        assertThat(AudiobookSpeed.bookIdOf(null, "x", null)).isNull()
    }
}
