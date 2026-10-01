package tv.onscreen.android.ui.playback

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.util.Locale

class SubtitleLabelTest {

    private fun label(
        language: String,
        title: String? = "",
        forced: Boolean = false,
        sdh: Boolean = false,
        downloaded: Boolean = false,
        locale: Locale = Locale.ENGLISH,
    ) = SubtitleLabel.of(language, title, forced, sdh, downloaded, fallback = "Track 3", locale = locale)

    @Test
    fun `the language by name, not by code`() {
        // ffprobe's ISO 639-2 codes, bibliographic ones included.
        assertThat(label("eng")).isEqualTo("English")
        assertThat(label("ger")).isEqualTo("German")
        assertThat(label("fre")).isEqualTo("French")
        // A download's 639-1 code, and its region.
        assertThat(label("es")).isEqualTo("Spanish")
        assertThat(label("pt-BR")).isEqualTo("Portuguese (Brazil)")
    }

    @Test
    fun `the language as the device names it`() {
        assertThat(label("eng", locale = Locale.GERMAN)).isEqualTo("Englisch")
    }

    @Test
    fun `then the title, unless it only repeats the language`() {
        assertThat(label("eng", "Commentary")).isEqualTo("English · Commentary")
        assertThat(label("eng", "English")).isEqualTo("English")
        assertThat(label("eng", "eng")).isEqualTo("English")
        assertThat(label("en-US", "English")).isEqualTo("English (United States)")
        // In English too on a device in another language.
        assertThat(label("eng", "English", locale = Locale.GERMAN)).isEqualTo("Englisch")
    }

    @Test
    fun `forced, SDH and downloaded, once each`() {
        assertThat(label("spa", forced = true)).isEqualTo("Spanish · forced")
        assertThat(label("eng", sdh = true)).isEqualTo("English · SDH")
        assertThat(label("eng", "English (Forced)", forced = true)).isEqualTo("English · English (Forced)")
        assertThat(label("eng", "SDH", sdh = true)).isEqualTo("English · SDH")
        assertThat(label("eng", "English CC", sdh = true)).isEqualTo("English · English CC")
        assertThat(label("eng", "Hearing Impaired", sdh = true)).isEqualTo("English · Hearing Impaired")
        assertThat(label("eng", forced = true, sdh = true, downloaded = true))
            .isEqualTo("English · forced · SDH · downloaded")
    }

    @Test
    fun `no language is left out, an unknown one kept as it came`() {
        assertThat(label("", "Director's notes")).isEqualTo("Director's notes")
        assertThat(label("und", "Signs")).isEqualTo("Signs")
        assertThat(label("qaa", "Signs")).isEqualTo("qaa · Signs")
    }

    @Test
    fun `neither a language nor a title falls back`() {
        assertThat(label("")).isEqualTo("Track 3")
        assertThat(label("und", null, forced = true)).isEqualTo("Track 3 · forced")
    }

    @Test
    fun `codes normalize to 639-1`() {
        assertThat(SubtitleLabel.normalizeLanguage("ENG")).isEqualTo("en")
        assertThat(SubtitleLabel.normalizeLanguage("en-US")).isEqualTo("en")
        assertThat(SubtitleLabel.normalizeLanguage("xyz")).isEqualTo("xyz")
        assertThat(SubtitleLabel.normalizeLanguage(null)).isEmpty()
    }
}
