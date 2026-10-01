package tv.onscreen.mobile.ui.player

import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.MimeTypes
import androidx.media3.common.Player
import androidx.media3.common.TrackGroup
import androidx.media3.common.Tracks
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import org.junit.Test
import tv.onscreen.mobile.data.model.AudioStream

/**
 * [AudioSelection.selectedRow]: which picker row shows as playing, against
 * real Media3 track objects. Only the Player is a stand-in.
 */
class AudioSelectionTest {

    private fun player(vararg groups: Tracks.Group): Player = mockk<Player>().also { p ->
        every { p.currentTracks } returns Tracks(groups.toList())
    }

    private fun audio(id: String, language: String?, selected: Boolean = false) = Tracks.Group(
        TrackGroup(id, Format.Builder().setId(id).setSampleMimeType(MimeTypes.AUDIO_AAC).setLanguage(language).build()),
        /* adaptiveSupported= */ false,
        intArrayOf(C.FORMAT_HANDLED),
        booleanArrayOf(selected),
    )

    private fun video() = Tracks.Group(
        TrackGroup("0:0", Format.Builder().setId("0:0").setSampleMimeType(MimeTypes.VIDEO_H264).build()),
        false,
        intArrayOf(C.FORMAT_HANDLED),
        booleanArrayOf(true),
    )

    // The file's audio as the API lists it: absolute ffprobe indexes 1..3.
    private val streams = listOf(
        AudioStream(1, "ac3", 6, "eng", "English 5.1"),
        AudioStream(2, "aac", 2, "eng", "Commentary"),
        AudioStream(3, "aac", 2, "jpn", "Japanese"),
    )

    @Test
    fun `a remux session shows the row it was started with, whatever its one track says`() {
        // The session's only audio track is selected: its place among the
        // player's tracks (0) says nothing about which row it is.
        val p = player(video(), audio("a", "ja", selected = true))
        assertThat(AudioSelection.selectedRow(p, streams, sessionRow = 2, hls = true)).isEqualTo(2)
        // The server's default before any pick: the first row.
        assertThat(AudioSelection.selectedRow(p, streams, sessionRow = 0, hls = true)).isEqualTo(0)
    }

    @Test
    fun `a remux session with no known row shows none`() {
        val p = player(audio("a", "en", selected = true))
        assertThat(AudioSelection.selectedRow(p, streams, sessionRow = null, hls = true)).isEqualTo(-1)
        assertThat(AudioSelection.selectedRow(p, streams, sessionRow = 3, hls = true)).isEqualTo(-1)
    }

    @Test
    fun `direct play shows the player's selected track by its place among the audio tracks`() {
        // Same-language tracks: only the place tells the commentary apart.
        val p = player(
            video(),
            audio("0:1", "en"),
            audio("0:2", "en", selected = true),
            audio("0:3", "ja"),
        )
        assertThat(AudioSelection.selectedRow(p, streams, sessionRow = null, hls = false)).isEqualTo(1)
    }

    @Test
    fun `direct play before the tracks load shows none`() {
        assertThat(AudioSelection.selectedRow(player(), streams, sessionRow = null, hls = false)).isEqualTo(-1)
        val p = player(video(), audio("0:1", "en"), audio("0:2", "en"), audio("0:3", "ja"))
        assertThat(AudioSelection.selectedRow(p, streams, sessionRow = null, hls = false)).isEqualTo(-1)
    }

    @Test
    fun `direct play with a track count that differs falls back to a language only one row has`() {
        // The player lists two audio tracks for the server's three.
        val japanese = player(audio("0:1", "en"), audio("0:3", "ja", selected = true))
        assertThat(AudioSelection.selectedRow(japanese, streams, sessionRow = null, hls = false)).isEqualTo(2)
        // English is two rows: no telling which.
        val english = player(audio("0:1", "en", selected = true), audio("0:3", "ja"))
        assertThat(AudioSelection.selectedRow(english, streams, sessionRow = null, hls = false)).isEqualTo(-1)
    }
}
