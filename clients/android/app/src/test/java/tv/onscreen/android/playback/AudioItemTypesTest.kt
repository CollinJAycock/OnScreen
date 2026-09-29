package tv.onscreen.android.playback

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class AudioItemTypesTest {

    @Test
    fun `tracks, books and chapter files play as audio`() {
        assertThat(AudioItemTypes.isAudio("track")).isTrue()
        assertThat(AudioItemTypes.isAudio("audiobook")).isTrue()
        // A multi-file book's chapter used to fall through to video: leaving
        // the player stopped the book, and it never chained to the next one.
        assertThat(AudioItemTypes.isAudio("audiobook_chapter")).isTrue()
    }

    @Test
    fun `video, containers and unknown types do not`() {
        listOf("movie", "episode", "music_video", "album", "podcast", "photo", "", null).forEach {
            assertThat(AudioItemTypes.isAudio(it)).isFalse()
        }
    }
}
