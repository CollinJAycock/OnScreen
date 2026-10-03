package tv.onscreen.android

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The Google TV build keeps both features the Fire TV build leaves out (see
 * productFlavors in app/build.gradle.kts). Its firetv twin is
 * src/testFiretv/.../FlavorFeaturesTest.kt.
 */
class FlavorFeaturesTest {

    @Test
    fun `google tv keeps online subtitle search`() {
        assertThat(BuildConfig.ONLINE_SUBTITLE_SEARCH).isTrue()
    }

    @Test
    fun `google tv keeps live tv and recordings`() {
        assertThat(BuildConfig.LIVE_TV).isTrue()
    }
}
