package tv.onscreen.mobile.ui.player

import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.MimeTypes
import androidx.media3.common.Player
import androidx.media3.common.TrackGroup
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.TrackSelectionParameters
import androidx.media3.common.Tracks
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import org.junit.Test

/**
 * [SubtitleSelection] against real Media3 track objects, shaped the way the
 * player reports them behind a MergingMediaSource: every child's format and
 * group ids come back prefixed with the child's index — "0:" for the media's
 * own (container) tracks, "1:sub:emb:3" for a side-load. Only the Player is
 * a stand-in, holding the tracks and the selection parameters.
 */
class SubtitleSelectionTest {

    private var params: TrackSelectionParameters = TrackSelectionParameters.DEFAULT
    private var paramWrites = 0

    private fun player(vararg groups: Tracks.Group): Player = mockk<Player>().also { p ->
        every { p.currentTracks } returns Tracks(groups.toList())
        every { p.trackSelectionParameters } answers { params }
        every { p.trackSelectionParameters = any() } answers {
            params = firstArg()
            paramWrites++
        }
    }

    /** A text track as the player lists it: [id] is the merged Format id. */
    private fun text(id: String, language: String?, selected: Boolean = false, mime: String = MimeTypes.TEXT_VTT) =
        Tracks.Group(
            TrackGroup(id, Format.Builder().setId(id).setSampleMimeType(mime).setLanguage(language).build()),
            /* adaptiveSupported= */ false,
            intArrayOf(C.FORMAT_HANDLED),
            booleanArrayOf(selected),
        )

    /** A container (Matroska) text track: its merged id is "0:{trackNumber}". */
    private fun container(trackNumber: Int, language: String?, selected: Boolean = false) =
        text("0:$trackNumber", language, selected, MimeTypes.APPLICATION_SUBRIP)

    private fun audio() = Tracks.Group(
        TrackGroup("0:1", Format.Builder().setId("0:1").setSampleMimeType(MimeTypes.AUDIO_AAC).build()),
        false,
        intArrayOf(C.FORMAT_HANDLED),
        booleanArrayOf(true),
    )

    private fun row(trackId: String, language: String, external: Boolean = false) = SubtitleTrack(
        trackId = trackId,
        language = language,
        title = "",
        forced = false,
        sdh = false,
        external = external,
        url = "http://srv/$trackId",
    )

    // Direct play: the file's two English streams (full, SDH) read from the
    // container, plus a downloaded Spanish file side-loaded next to it.
    private val directRows = listOf(
        row("sub:emb:3", "eng"),
        row("sub:emb:4", "eng"),
        row("sub:ext:e1", "spa", external = true),
    )

    @Test
    fun `a selected side-load is found through its prefixed id`() {
        val p = player(audio(), container(3, "en"), container(4, "en"), text("1:sub:ext:e1", "es", selected = true))
        assertThat(SubtitleSelection.selectedTrackId(p, directRows)).isEqualTo("sub:ext:e1")
    }

    @Test
    fun `a selected container track is told apart from its same-language twin by position`() {
        val p = player(audio(), container(3, "en"), container(4, "en", selected = true), text("1:sub:ext:e1", "es"))
        assertThat(SubtitleSelection.selectedTrackId(p, directRows)).isEqualTo("sub:emb:4")
    }

    @Test
    fun `nothing selected reads as off`() {
        val p = player(audio(), container(3, "en"), container(4, "en"), text("1:sub:ext:e1", "es"))
        assertThat(SubtitleSelection.selectedTrackId(p, directRows)).isNull()
    }

    @Test
    fun `picking the SDH twin overrides exactly its group and turns text on`() {
        params = params.buildUpon().setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true).build()
        val sdh = container(4, "en")
        val p = player(audio(), container(3, "en"), sdh, text("1:sub:ext:e1", "es"))

        assertThat(SubtitleSelection.apply(p, SubtitleChoice.Track("sub:emb:4"), directRows)).isTrue()

        assertThat(params.disabledTrackTypes).doesNotContain(C.TRACK_TYPE_TEXT)
        assertThat(params.overrides.values.filter { it.type == C.TRACK_TYPE_TEXT })
            .containsExactly(TrackSelectionOverride(sdh.mediaTrackGroup, 0))
    }

    @Test
    fun `picking a side-load finds it behind the merge prefix`() {
        val spanish = text("2:sub:ext:e1", "es")
        val p = player(audio(), container(3, "en"), container(4, "en"), spanish)

        assertThat(SubtitleSelection.apply(p, SubtitleChoice.Track("sub:ext:e1"), directRows)).isTrue()

        assertThat(params.overrides[spanish.mediaTrackGroup]?.trackIndices).containsExactly(0)
    }

    @Test
    fun `an HLS session's same-language side-loads are each reachable`() {
        // HLS: the session has no text of its own; every row is a side-load.
        val rows = listOf(row("sub:emb:3", "eng"), row("sub:emb:4", "eng"))
        val full = text("1:sub:emb:3", "en")
        val sdh = text("2:sub:emb:4", "en")
        val p = player(audio(), full, sdh)

        assertThat(SubtitleSelection.apply(p, SubtitleChoice.Track("sub:emb:4"), rows)).isTrue()
        assertThat(params.overrides.keys).containsExactly(sdh.mediaTrackGroup)

        assertThat(SubtitleSelection.apply(p, SubtitleChoice.Track("sub:emb:3"), rows)).isTrue()
        assertThat(params.overrides.keys).containsExactly(full.mediaTrackGroup)
    }

    @Test
    fun `a track the player doesn't have yet is reported for a retry, and nothing changes`() {
        // The downloaded file's side-load hasn't been merged in yet.
        val p = player(audio(), container(3, "en"), container(4, "en"))

        assertThat(SubtitleSelection.apply(p, SubtitleChoice.Track("sub:ext:e1"), directRows)).isFalse()
        assertThat(paramWrites).isEqualTo(0)
    }

    @Test
    fun `a choice already in effect is left alone, so the tracks listener settles`() {
        val sdh = container(4, "en", selected = true)
        val p = player(audio(), container(3, "en"), sdh)
        SubtitleSelection.apply(p, SubtitleChoice.Track("sub:emb:4"), directRows)
        assertThat(paramWrites).isEqualTo(1)

        assertThat(SubtitleSelection.apply(p, SubtitleChoice.Track("sub:emb:4"), directRows)).isTrue()
        assertThat(paramWrites).isEqualTo(1)
    }

    @Test
    fun `off disables text and drops the text override, once`() {
        val sdh = container(4, "en", selected = true)
        val p = player(audio(), container(3, "en"), sdh)
        SubtitleSelection.apply(p, SubtitleChoice.Track("sub:emb:4"), directRows)

        assertThat(SubtitleSelection.apply(p, SubtitleChoice.Off, directRows)).isTrue()
        assertThat(params.disabledTrackTypes).contains(C.TRACK_TYPE_TEXT)
        assertThat(params.overrides.values.none { it.type == C.TRACK_TYPE_TEXT }).isTrue()
        val writes = paramWrites

        SubtitleSelection.apply(p, SubtitleChoice.Off, directRows)
        assertThat(paramWrites).isEqualTo(writes)
    }
}
