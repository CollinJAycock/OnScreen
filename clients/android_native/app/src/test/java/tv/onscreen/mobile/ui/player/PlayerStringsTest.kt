package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.model.PlaybackStop
import tv.onscreen.mobile.i18n.UiText

/**
 * The player's text moved into string resources (strings_player.xml): the
 * English is what the code used to build, and the placeholders take the
 * values the screens pass.
 */
class PlayerStringsTest {

    @Test
    fun `placeholders fill as the screens used to build them`() {
        assertThat(EnglishText.string(R.string.chromecast_casting_to, "Living Room")).isEqualTo("Casting to Living Room")
        assertThat(EnglishText.string(R.string.player_sleep_minutes, 15)).isEqualTo("15m")
        assertThat(EnglishText.string(R.string.player_sleep_active, "14:59")).isEqualTo("Active: 14:59")
        assertThat(EnglishText.string(R.string.player_volume_percent, 50)).isEqualTo("50%")
        assertThat(EnglishText.string(R.string.player_replaygain_applied, "-6.2"))
            .isEqualTo("ReplayGain -6.2 decibels applied")
        assertThat(EnglishText.string(R.string.player_subtitle_track_n, "3")).isEqualTo("Track 3")
        assertThat(EnglishText.string(R.string.player_subtitle_burned_in, "eng · Signs"))
            .isEqualTo("eng · Signs (burned in)")
    }

    @Test
    fun `the player's errors read as before`() {
        assertThat(START_FAILED_MESSAGE.en()).isEqualTo("Couldn’t start playback.")
        assertThat(DAMAGED_TITLE.en()).isEqualTo("This file appears to be damaged")
        assertThat(DAMAGED_MESSAGE.en()).isEqualTo(
            "The integrity check could not decode parts of this file, so playback " +
                "would stall or show broken video. Replace the file with a good copy and re-scan the library.",
        )
        assertThat(DOLBY_VISION_UNSUPPORTED.en()).isEqualTo("Dolby Vision is not supported")
        assertThat(EnglishText.string(R.string.player_cellular_body))
            .isEqualTo("You're on a metered connection. Streaming video may use a significant amount of data.")
    }

    @Test
    fun `an admin's note goes inside the sentence, in any language`() {
        assertThat(PlaybackStop.text(" maintenance ")).isEqualTo(
            UiText.res(R.string.playback_stopped_by_admin_note, "maintenance"),
        )
        assertThat(PlaybackStop.text(null)).isEqualTo(UiText.res(R.string.playback_stopped_by_admin))
    }

    @Test
    fun `a subtitle label takes its words from the language it is given`() {
        // A stand-in translation: the words come from the resolver, the
        // file's language code and title stay as the file has them.
        val words = SubtitleTracks.Words { id, args ->
            when (id) {
                R.string.player_subtitle_forced -> "forzados"
                R.string.player_subtitle_burned_in -> "${args[0]} (incrustados)"
                else -> EnglishText.string(id, *args)
            }
        }
        val track = SubtitleTrack(
            trackId = "sub:emb:4",
            language = "spa",
            title = "Signs",
            forced = true,
            sdh = false,
            external = false,
            imageBased = true,
            streamIndex = 4,
            url = null,
            textTrackId = null,
        )
        assertThat(SubtitleTracks.label(track, burnedIn = true, words = words))
            .isEqualTo("spa · Signs · forzados (incrustados)")
        assertThat(SubtitleTracks.label(track.copy(language = "", title = "", forced = false), words = EnglishText.words))
            .isEqualTo("Track 4")
    }
}
