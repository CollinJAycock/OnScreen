package tv.onscreen.mobile.ui.components

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.R

/** The kinds the server sends map to their own string; others show as sent. */
class TypeNamesTest {

    @Test
    fun `known kinds of library, item and collection have a string`() {
        assertThat(wireTypeRes("movie")).isEqualTo(R.string.library_type_movie)
        assertThat(wireTypeRes("music_video")).isEqualTo(R.string.library_type_music_video)
        assertThat(wireTypeRes("home_video")).isEqualTo(R.string.library_type_home_video)
        assertThat(wireTypeRes("smart_playlist")).isEqualTo(R.string.library_type_smart_playlist)
        assertThat(wireTypeRes("franchise")).isEqualTo(R.string.library_type_franchise)
    }

    @Test
    fun `an unknown kind has none`() {
        assertThat(wireTypeRes("hologram")).isNull()
        assertThat(wireTypeRes("")).isNull()
    }
}
