package tv.onscreen.mobile.ui.watch

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Multi-episode files: one item covering index..index_end. */
class EpisodeNumberingTest {

    @Test
    fun `a single episode is its number`() {
        assertThat(episodeNumberLabel(3, null)).isEqualTo("3")
        assertThat(lastEpisodeIndex(3, null)).isEqualTo(3)
    }

    @Test
    fun `a multi-episode file reads as a range`() {
        assertThat(episodeNumberLabel(1, 2)).isEqualTo("1–2")
        assertThat(lastEpisodeIndex(1, 2)).isEqualTo(2)
    }

    @Test
    fun `an index_end that doesn't run forward is ignored`() {
        // Equal (a server that echoes the index) or backwards (bad data).
        assertThat(episodeNumberLabel(4, 4)).isEqualTo("4")
        assertThat(episodeNumberLabel(4, 2)).isEqualTo("4")
        assertThat(lastEpisodeIndex(4, 2)).isEqualTo(4)
    }
}
