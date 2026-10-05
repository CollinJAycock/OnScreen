package tv.onscreen.android.ui.search

import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ResolveInfo
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import org.junit.Test

/**
 * The Search screen's microphone shows only where a speech recognizer is
 * installed: the Amazon Appstore rejected the Fire TV build because the orb
 * did nothing on a Fire TV without one.
 */
class VoiceSearchTest {

    private fun packageManager(recognizers: List<ResolveInfo>): PackageManager =
        mockk { every { queryIntentActivities(any<Intent>(), any<Int>()) } returns recognizers }

    @Test
    fun `offered where a recognizer is installed, Fire TV or not`() {
        assertThat(VoiceSearch.recognizerInstalled(packageManager(listOf(ResolveInfo())))).isTrue()
    }

    @Test
    fun `not offered without a recognizer, so the orb is never dead`() {
        assertThat(VoiceSearch.recognizerInstalled(packageManager(emptyList()))).isFalse()
    }

    @Test
    fun `not offered when the lookup fails`() {
        val pm = mockk<PackageManager> {
            every { queryIntentActivities(any<Intent>(), any<Int>()) } throws SecurityException()
        }
        assertThat(VoiceSearch.recognizerInstalled(pm)).isFalse()
    }

    @Test
    fun `a heard query is searched`() {
        assertThat(VoiceSearch.outcome(resultCode = OK, matches = listOf("  sintel "), elapsedMs = 4000))
            .isEqualTo(VoiceSearch.Outcome.Query("sintel"))
    }

    @Test
    fun `a recognizer that comes straight back never listened, and says so`() {
        assertThat(VoiceSearch.outcome(resultCode = CANCELED, matches = null, elapsedMs = 100))
            .isEqualTo(VoiceSearch.Outcome.Unavailable)
        assertThat(VoiceSearch.outcome(resultCode = OK, matches = emptyList(), elapsedMs = 300))
            .isEqualTo(VoiceSearch.Outcome.Unavailable)
    }

    @Test
    fun `a viewer backing out quickly on a working recognizer is a cancel, not unavailable`() {
        assertThat(VoiceSearch.outcome(resultCode = CANCELED, matches = null, elapsedMs = 800))
            .isEqualTo(VoiceSearch.Outcome.Cancelled)
    }

    @Test
    fun `listened but heard nothing`() {
        assertThat(VoiceSearch.outcome(resultCode = OK, matches = listOf(""), elapsedMs = 6000))
            .isEqualTo(VoiceSearch.Outcome.NothingHeard)
    }

    @Test
    fun `backing out after a while is just a cancel`() {
        assertThat(VoiceSearch.outcome(resultCode = CANCELED, matches = null, elapsedMs = 6000))
            .isEqualTo(VoiceSearch.Outcome.Cancelled)
    }

    // The recognizer's own error codes used to read as a cancel: no toast,
    // and the press looked dead.
    @Test
    fun `a recognizer's no-match is nothing heard, and its errors say so`() {
        assertThat(VoiceSearch.outcome(resultCode = 1, matches = null, elapsedMs = 3000))
            .isEqualTo(VoiceSearch.Outcome.NothingHeard)
        for (code in 2..5) { // client, server, network, audio
            assertThat(VoiceSearch.outcome(resultCode = code, matches = null, elapsedMs = 3000))
                .isEqualTo(VoiceSearch.Outcome.Failed)
        }
    }

    private companion object {
        const val OK = -1 // Activity.RESULT_OK
        const val CANCELED = 0
    }
}
