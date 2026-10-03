package tv.onscreen.android.ui.search

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The Search screen's microphone shows only where it can work: the Amazon
 * Appstore rejected the Fire TV build because the orb did nothing there
 * (Fire OS offers apps no speech recognizer).
 */
class VoiceSearchTest {

    @Test
    fun `offered when the build allows it and a recognizer is installed`() {
        assertThat(VoiceSearch.enabled(buildAllows = true, recognizerInstalled = true)).isTrue()
    }

    @Test
    fun `not offered without a recognizer, so the orb is never dead`() {
        assertThat(VoiceSearch.enabled(buildAllows = true, recognizerInstalled = false)).isFalse()
    }

    @Test
    fun `not offered where the build leaves it out, as on Fire TV, recognizer or not`() {
        assertThat(VoiceSearch.enabled(buildAllows = false, recognizerInstalled = true)).isFalse()
        assertThat(VoiceSearch.enabled(buildAllows = false, recognizerInstalled = false)).isFalse()
    }
}
