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

    @Test
    fun `a player keeps its speed from one chapter of a book to the next`() {
        // The background service's chapter chain: same book, so no lookup.
        assertThat(AudiobookSpeed.keepsSpeed("book-1", "book-1")).isTrue()
        // Into the next book of a series: that book's own speed.
        assertThat(AudiobookSpeed.keepsSpeed("book-1", "book-2")).isFalse()
        // Out of books (or into one from music): re-decided.
        assertThat(AudiobookSpeed.keepsSpeed("book-1", null)).isFalse()
        assertThat(AudiobookSpeed.keepsSpeed(null, "book-1")).isFalse()
        assertThat(AudiobookSpeed.keepsSpeed(null, null)).isFalse()
    }

    @Test
    fun `a chapter-to-chapter chain carries the book's speed`() {
        assertThat(AudiobookSpeed.carried("audiobook_chapter", "book-1", "audiobook_chapter", 1.75f))
            .isEqualTo(BookSpeed("book-1", 1.75f))
        // Clamped to what the server would store.
        assertThat(AudiobookSpeed.carried("audiobook_chapter", "book-1", "audiobook_chapter", 9f))
            .isEqualTo(BookSpeed("book-1", 3.0f))
    }

    @Test
    fun `nothing else carries a speed`() {
        // Tracks play at 1×; a single-file book's next book has its own speed.
        assertThat(AudiobookSpeed.carried("track", null, "track", 1.5f)).isNull()
        assertThat(AudiobookSpeed.carried("audiobook", "book-1", "audiobook", 1.5f)).isNull()
        assertThat(AudiobookSpeed.carried("audiobook_chapter", "book-1", "track", 1.5f)).isNull()
        // An orphan chapter has no book to carry it for.
        assertThat(AudiobookSpeed.carried("audiobook_chapter", null, "audiobook_chapter", 1.5f)).isNull()
    }
}
