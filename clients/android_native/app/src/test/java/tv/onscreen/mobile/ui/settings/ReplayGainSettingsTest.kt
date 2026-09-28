package tv.onscreen.mobile.ui.settings

import com.google.common.truth.Truth.assertThat
import io.mockk.coVerify
import io.mockk.every
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.prefs.PlaybackPrefs
import tv.onscreen.mobile.playback.ReplayGainMode

@OptIn(ExperimentalCoroutinesApi::class)
class ReplayGainSettingsTest {

    private val dispatcher = UnconfinedTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    private fun vm(prefs: PlaybackPrefs) = SettingsViewModel(
        prefs = prefs,
        server = mockk(relaxed = true),
        auth = mockk(relaxed = true),
        downloads = mockk(relaxed = true),
        appContext = mockk(relaxed = true),
    )

    @Test
    fun `exposes the stored mode and preamp`() = runTest(dispatcher) {
        val prefs = mockk<PlaybackPrefs>(relaxed = true)
        every { prefs.replayGainMode } returns flowOf(ReplayGainMode.ALBUM)
        every { prefs.replayGainPreampDb } returns flowOf(-1.5)
        val vm = vm(prefs)
        assertThat(vm.replayGainMode.first()).isEqualTo(ReplayGainMode.ALBUM)
        assertThat(vm.replayGainPreampDb.first()).isEqualTo(-1.5)
    }

    @Test
    fun `writes mode and preamp through to PlaybackPrefs`() = runTest(dispatcher) {
        val prefs = mockk<PlaybackPrefs>(relaxed = true)
        val vm = vm(prefs)
        vm.setReplayGainMode(ReplayGainMode.TRACK)
        vm.setReplayGainPreampDb(2.5)
        advanceUntilIdle()
        coVerify { prefs.setReplayGainMode(ReplayGainMode.TRACK) }
        coVerify { prefs.setReplayGainPreampDb(2.5) }
    }

    @Test
    fun `preamp label reads like a level`() {
        assertThat(formatPreamp(0.0)).isEqualTo("0 dB")
        assertThat(formatPreamp(1.5)).isEqualTo("+1.5 dB")
        assertThat(formatPreamp(6.0)).isEqualTo("+6 dB")
        assertThat(formatPreamp(-3.0)).isEqualTo("−3 dB")
        // Off-grid values are shown snapped, the way they'll be stored.
        assertThat(formatPreamp(-0.3)).isEqualTo("−0.5 dB")
        assertThat(formatPreamp(42.0)).isEqualTo("+6 dB")
    }
}
