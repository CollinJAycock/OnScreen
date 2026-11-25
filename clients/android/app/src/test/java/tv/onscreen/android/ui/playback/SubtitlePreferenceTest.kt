package tv.onscreen.android.ui.playback

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.android.data.model.AudioStream
import tv.onscreen.android.data.model.SubtitleMode
import tv.onscreen.android.data.model.SubtitleStream
import tv.onscreen.android.i18n.UiText

/**
 * The saved language preferences against one file: the subtitle that comes
 * on by itself in each mode (docs/foreign-audio-subtitles.md §2, the web's
 * subtitle-select.test.ts table), picked as itself — Media3's own ranking
 * puts a default-flagged and then a full track above a forced one once the
 * language matches (DefaultTrackSelector.TextTrackInfo), so English with
 * forced-only used to show the FULL English subtitles on a film with both —
 * and the audio track a server session starts with.
 */
class SubtitlePreferenceTest {

    private fun stream(index: Int, language: String, forced: Boolean = false, sdh: Boolean = false) =
        SubtitleStream(index = index, codec = "subrip", language = language, title = "", forced = forced, sdh = sdh)

    private fun source(trackId: String, language: String, forced: Boolean = false, embeddedIndex: Int? = null) =
        SubtitleTrackSource(
            url = "https://srv/sub/$trackId", language = language, label = UiText.Raw(trackId),
            forced = forced, trackId = trackId, embeddedIndex = embeddedIndex,
        )

    private fun audio(language: String) = AudioStream(index = 1, codec = "aac", channels = 2, language = language, title = "")

    /** Direct play of [streams], preferring English, the audio in [audio]. */
    private fun direct(streams: List<SubtitleStream>, mode: SubtitleMode, audio: String?, sources: List<SubtitleTrackSource> = emptyList()) =
        preferredSubtitleChoice(false, streams, sources, "eng", mode, audio)

    private val fullAndForced = listOf(stream(2, "eng"), stream(3, "eng", forced = true))

    // ── forced ────────────────────────────────────────────────────────────

    @Test
    fun `forced picks the forced container track by ordinal, not the full one`() {
        val streams = listOf(stream(2, "eng"), stream(3, "eng", forced = true), stream(4, "spa", forced = true))
        assertThat(direct(streams, SubtitleMode.FORCED, "eng")).isEqualTo("ord:1")
        // "en" and "eng" are the same language.
        assertThat(preferredSubtitleChoice(false, streams, emptyList(), "en", SubtitleMode.FORCED, "eng")).isEqualTo("ord:1")
        // Whatever the audio.
        assertThat(direct(streams, SubtitleMode.FORCED, "jpn")).isEqualTo("ord:1")
    }

    @Test
    fun `forced with no forced track in the language leaves subtitles off`() {
        assertThat(direct(listOf(stream(2, "eng"), stream(3, "spa", forced = true)), SubtitleMode.FORCED, "jpn")).isNull()
    }

    @Test
    fun `direct play falls back to a forced attached file`() {
        val sources = listOf(source("sub:ext:9", "eng", forced = true))
        assertThat(direct(listOf(stream(2, "eng")), SubtitleMode.FORCED, "eng", sources)).isEqualTo("id:sub:ext:9")
    }

    @Test
    fun `a server session picks the side-load by its id`() {
        val sources = listOf(
            source("sub:emb:2", "eng", embeddedIndex = 2),
            source("sub:emb:3", "eng", forced = true, embeddedIndex = 3),
        )
        assertThat(preferredSubtitleChoice(true, emptyList(), sources, "eng", SubtitleMode.FORCED, "eng")).isEqualTo("id:sub:emb:3")
        assertThat(preferredSubtitleChoice(true, emptyList(), sources.take(1), "eng", SubtitleMode.FORCED, "eng")).isNull()
        assertThat(preferredSubtitleChoice(true, emptyList(), sources, "eng", SubtitleMode.FOREIGN, "jpn")).isEqualTo("id:sub:emb:2")
    }

    // ── foreign (the doc's §2 table) ──────────────────────────────────────

    @Test
    fun `foreign with audio in the subtitle language is forced-only`() {
        assertThat(direct(fullAndForced, SubtitleMode.FOREIGN, "eng")).isEqualTo("ord:1")
        assertThat(direct(listOf(stream(2, "eng")), SubtitleMode.FOREIGN, "eng")).isNull()
        // "en" audio, "eng" subtitles: the same language.
        assertThat(direct(fullAndForced, SubtitleMode.FOREIGN, "en")).isEqualTo("ord:1")
    }

    @Test
    fun `foreign with audio in another language shows the full track`() {
        assertThat(direct(fullAndForced, SubtitleMode.FOREIGN, "jpn")).isEqualTo("ord:0")
        assertThat(direct(listOf(stream(2, "eng")), SubtitleMode.FOREIGN, "jpn")).isEqualTo("ord:0")
        // Only a forced one: better than nothing.
        assertThat(direct(listOf(stream(3, "eng", forced = true)), SubtitleMode.FOREIGN, "jpn")).isEqualTo("ord:0")
        // None in the language: off.
        assertThat(direct(listOf(stream(2, "spa")), SubtitleMode.FOREIGN, "jpn")).isNull()
    }

    @Test
    fun `foreign treats untagged or mixed audio as not foreign`() {
        for (tag in listOf("und", "", "mul", "zxx", "mis", null)) {
            assertThat(direct(fullAndForced, SubtitleMode.FOREIGN, tag)).isEqualTo("ord:1")
            assertThat(direct(listOf(stream(2, "eng")), SubtitleMode.FOREIGN, tag)).isNull()
        }
    }

    // ── always ────────────────────────────────────────────────────────────

    @Test
    fun `always picks the full track before the forced one, plain before SDH`() {
        val streams = listOf(stream(2, "eng", forced = true), stream(3, "eng", sdh = true), stream(4, "eng"))
        assertThat(direct(streams, SubtitleMode.ALWAYS, "eng")).isEqualTo("ord:2")
        assertThat(direct(streams.take(2), SubtitleMode.ALWAYS, "eng")).isEqualTo("ord:1")
        assertThat(direct(streams.take(1), SubtitleMode.ALWAYS, "eng")).isEqualTo("ord:0")
        assertThat(direct(listOf(stream(2, "spa")), SubtitleMode.ALWAYS, "eng")).isNull()
    }

    @Test
    fun `direct play reaches an attached full file after the container's tracks`() {
        val sources = listOf(
            source("sub:emb:3", "eng", forced = true, embeddedIndex = 3), // the container's own: by ordinal
            source("sub:ext:9", "eng"),
        )
        assertThat(direct(listOf(stream(3, "eng", forced = true)), SubtitleMode.ALWAYS, "eng", sources)).isEqualTo("id:sub:ext:9")
        assertThat(direct(listOf(stream(3, "eng", forced = true)), SubtitleMode.FORCED, "eng", sources)).isEqualTo("ord:0")
    }

    // ── the pieces ────────────────────────────────────────────────────────

    @Test
    fun `foreign audio is a known language other than the subtitles'`() {
        assertThat(isForeignAudio("jpn", "en")).isTrue()
        assertThat(isForeignAudio("fra", "eng")).isTrue()
        assertThat(isForeignAudio("eng", "en")).isFalse()
        assertThat(isForeignAudio("en-US", "eng")).isFalse()
        assertThat(isForeignAudio("und", "en")).isFalse()
        assertThat(isForeignAudio(null, "en")).isFalse()
    }

    @Test
    fun `the preferred audio is the first track in the language`() {
        val streams = listOf(audio("jpn"), audio("eng"), audio("eng"))
        assertThat(pickPreferredAudio(streams, "eng")).isEqualTo(1)
        assertThat(pickPreferredAudio(streams, "en")).isEqualTo(1)
        assertThat(pickPreferredAudio(streams, "ja")).isEqualTo(0)
        // None in it, or no preference: the server's default.
        assertThat(pickPreferredAudio(streams, "fre")).isNull()
        assertThat(pickPreferredAudio(streams, null)).isNull()
        assertThat(pickPreferredAudio(streams, "")).isNull()
    }
}
