package tv.onscreen.mobile.playback

import android.app.SearchManager
import android.content.Intent
import android.provider.MediaStore
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import org.junit.Test

/** "Play … on OnScreen" from the Assistant, and the same intent handed back
 *  when the app is reopened from Recents — which must not play again. */
class VoicePlayTest {

    private fun intent(action: String?, flags: Int = 0, query: String? = "jazz"): Intent =
        mockk<Intent>().also {
            every { it.action } returns action
            every { it.flags } returns flags
            every { it.getStringExtra(SearchManager.QUERY) } returns query
        }

    @Test
    fun `a play-from-search intent carries its query`() {
        assertThat(VoicePlay.query(intent(MediaStore.INTENT_ACTION_MEDIA_PLAY_FROM_SEARCH))).isEqualTo("jazz")
        assertThat(VoicePlay.query(intent(MediaStore.INTENT_ACTION_MEDIA_PLAY_FROM_SEARCH, query = null))).isEqualTo("")
    }

    @Test
    fun `a relaunch from Recents is not a new request`() {
        val relaunch = intent(
            MediaStore.INTENT_ACTION_MEDIA_PLAY_FROM_SEARCH,
            flags = Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY or Intent.FLAG_ACTIVITY_NEW_TASK,
        )
        assertThat(VoicePlay.query(relaunch)).isNull()
    }

    @Test
    fun `other intents are not voice requests`() {
        assertThat(VoicePlay.query(intent(Intent.ACTION_MAIN))).isNull()
        assertThat(VoicePlay.query(null)).isNull()
    }
}
