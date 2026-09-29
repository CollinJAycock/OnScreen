package tv.onscreen.android.playback

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class AudiobookSpeedTest {

    @Test
    fun `presets run from three-quarter to triple speed, slowest first`() {
        assertThat(AudiobookSpeed.PRESETS)
            .containsExactly(0.75f, 1.0f, 1.25f, 1.5f, 1.75f, 2.0f, 2.5f, 3.0f)
            .inOrder()
        // Every preset is a speed the server accepts.
        AudiobookSpeed.PRESETS.forEach { assertThat(AudiobookSpeed.clamp(it)).isEqualTo(it) }
    }

    @Test
    fun `clamp keeps a rate inside the server's range, to two decimals`() {
        assertThat(AudiobookSpeed.clamp(0.1)).isEqualTo(0.5f)
        assertThat(AudiobookSpeed.clamp(7.0)).isEqualTo(3.0f)
        assertThat(AudiobookSpeed.clamp(1.2500000476837158)).isEqualTo(1.25f)
        assertThat(AudiobookSpeed.clamp(1.333)).isEqualTo(1.33f)
        assertThat(AudiobookSpeed.clamp(Double.NaN)).isEqualTo(1.0f)
        assertThat(AudiobookSpeed.clamp(Double.POSITIVE_INFINITY)).isEqualTo(1.0f)
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
        } finally {
            java.util.Locale.setDefault(saved)
        }
    }

    @Test
    fun `presetIndex finds the preset a speed is at, or none`() {
        assertThat(AudiobookSpeed.presetIndex(1.0f)).isEqualTo(1)
        assertThat(AudiobookSpeed.presetIndex(1.5000001f)).isEqualTo(3)
        // Set on another client, between presets.
        assertThat(AudiobookSpeed.presetIndex(1.1f)).isEqualTo(-1)
    }

    @Test
    fun `only audiobooks and their chapters have a speed`() {
        assertThat(AudiobookSpeed.hasSpeed("audiobook")).isTrue()
        assertThat(AudiobookSpeed.hasSpeed("audiobook_chapter")).isTrue()
        listOf("track", "album", "podcast", "episode", "movie", null).forEach {
            assertThat(AudiobookSpeed.hasSpeed(it)).isFalse()
        }
    }

    @Test
    fun `a book's speed is its own, a chapter's is its book's`() {
        assertThat(AudiobookSpeed.bookIdOf("audiobook", "b1", "author-1")).isEqualTo("b1")
        assertThat(AudiobookSpeed.bookIdOf("audiobook_chapter", "c3", "b1")).isEqualTo("b1")
        // An orphan chapter can't be resolved; music has no book at all.
        assertThat(AudiobookSpeed.bookIdOf("audiobook_chapter", "c3", null)).isNull()
        assertThat(AudiobookSpeed.bookIdOf("track", "t1", "album-1")).isNull()
    }
}
