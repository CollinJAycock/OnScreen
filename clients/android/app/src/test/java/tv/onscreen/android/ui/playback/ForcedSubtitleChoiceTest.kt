package tv.onscreen.android.ui.playback

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.android.data.model.SubtitleStream
import tv.onscreen.android.i18n.UiText

/**
 * The forced-only preference picks the forced track itself: Media3's own
 * ranking puts a full track above a forced one once the language matches
 * (DefaultTrackSelector.TextTrackInfo), so English with forced-only on used
 * to show the FULL English subtitles on a film that has both.
 */
class ForcedSubtitleChoiceTest {

    private fun stream(index: Int, language: String, forced: Boolean = false) =
        SubtitleStream(index = index, codec = "subrip", language = language, title = "", forced = forced)

    private fun source(trackId: String, language: String, forced: Boolean = false, embeddedIndex: Int? = null) =
        SubtitleTrackSource(
            url = "https://srv/sub/$trackId", language = language, label = UiText.Raw(trackId),
            forced = forced, trackId = trackId, embeddedIndex = embeddedIndex,
        )

    @Test
    fun `direct play picks the forced container track by ordinal, not the full one`() {
        val streams = listOf(stream(2, "eng"), stream(3, "eng", forced = true), stream(4, "spa", forced = true))
        assertThat(forcedSubtitleChoice(false, streams, emptyList(), "eng")).isEqualTo("ord:1")
        // "en" and "eng" are the same language.
        assertThat(forcedSubtitleChoice(false, streams, emptyList(), "en")).isEqualTo("ord:1")
    }

    @Test
    fun `no forced track in the language leaves subtitles off`() {
        val streams = listOf(stream(2, "eng"), stream(3, "spa", forced = true))
        assertThat(forcedSubtitleChoice(false, streams, emptyList(), "eng")).isNull()
    }

    @Test
    fun `direct play falls back to a forced attached file`() {
        val sources = listOf(source("sub:ext:9", "eng", forced = true))
        assertThat(forcedSubtitleChoice(false, listOf(stream(2, "eng")), sources, "eng")).isEqualTo("id:sub:ext:9")
    }

    @Test
    fun `a server session picks the forced side-load by its id`() {
        val sources = listOf(
            source("sub:emb:2", "eng", embeddedIndex = 2),
            source("sub:emb:3", "eng", forced = true, embeddedIndex = 3),
        )
        assertThat(forcedSubtitleChoice(true, emptyList(), sources, "eng")).isEqualTo("id:sub:emb:3")
        assertThat(forcedSubtitleChoice(true, emptyList(), sources.take(1), "eng")).isNull()
    }
}
