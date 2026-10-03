package tv.onscreen.android

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The Fire TV build leaves out the two features the Amazon Appstore's
 * "save, convert, stream or download media from third-party sources" policy
 * matches (see productFlavors in app/build.gradle.kts). Its googletv twin is
 * src/testGoogletv/.../FlavorFeaturesTest.kt.
 */
class FlavorFeaturesTest {

    @Test
    fun `fire tv has no online subtitle search`() {
        assertThat(BuildConfig.ONLINE_SUBTITLE_SEARCH).isFalse()
    }

    @Test
    fun `fire tv has no live tv or recordings`() {
        assertThat(BuildConfig.LIVE_TV).isFalse()
    }

    @Test
    fun `fire tv has no search microphone, Fire OS offers apps no speech recognizer`() {
        assertThat(BuildConfig.VOICE_SEARCH).isFalse()
    }
}
