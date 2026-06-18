package tv.onscreen.android.ui.settings

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.android.data.model.ScrobbleStatusResponse
import tv.onscreen.android.data.model.UserPreferences
import tv.onscreen.android.data.repository.AuthRepository
import tv.onscreen.android.data.repository.PreferencesRepository
import tv.onscreen.android.data.repository.ScrobbleRepository

@OptIn(ExperimentalCoroutinesApi::class)
class SettingsViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() { Dispatchers.setMain(dispatcher) }

    @After
    fun tearDown() { Dispatchers.resetMain() }

    // load() always probes scrobble status; stub it so the other assertions
    // aren't disturbed.
    private fun scrobbleRepo(status: ScrobbleStatusResponse = ScrobbleStatusResponse()) =
        mockk<ScrobbleRepository>().also { coEvery { it.status() } returns status }

    @Test
    fun `load fetches preferences and stores identity`() = runTest(dispatcher) {
        val prefsRepo = mockk<PreferencesRepository>()
        val authRepo = mockk<AuthRepository>()
        val saved = UserPreferences(preferred_audio_lang = "en")
        coEvery { prefsRepo.get() } returns saved

        val vm = SettingsViewModel(prefsRepo, authRepo, scrobbleRepo())
        vm.load(username = "alice", serverUrl = "http://server")
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.preferences).isEqualTo(saved)
        assertThat(state.username).isEqualTo("alice")
        assertThat(state.serverUrl).isEqualTo("http://server")
        assertThat(state.loading).isFalse()
        assertThat(state.error).isNull()
    }

    @Test
    fun `load reflects linked scrobble status`() = runTest(dispatcher) {
        val prefsRepo = mockk<PreferencesRepository>()
        val authRepo = mockk<AuthRepository>()
        coEvery { prefsRepo.get() } returns UserPreferences()

        val vm = SettingsViewModel(
            prefsRepo,
            authRepo,
            scrobbleRepo(ScrobbleStatusResponse(listenbrainz_linked = true, listenbrainz_enabled = true)),
        )
        vm.load(null, null)
        advanceUntilIdle()

        assertThat(vm.uiState.value.scrobbleLinked).isTrue()
        assertThat(vm.uiState.value.scrobbleEnabled).isTrue()
    }

    @Test
    fun `load records error when preferences fetch fails`() = runTest(dispatcher) {
        val prefsRepo = mockk<PreferencesRepository>()
        val authRepo = mockk<AuthRepository>()
        coEvery { prefsRepo.get() } throws RuntimeException("boom")

        val vm = SettingsViewModel(prefsRepo, authRepo, scrobbleRepo())
        vm.load(null, null)
        advanceUntilIdle()

        assertThat(vm.uiState.value.error).isEqualTo("boom")
        assertThat(vm.uiState.value.loading).isFalse()
    }

    @Test
    fun `savePreferences updates state and sets saved flag`() = runTest(dispatcher) {
        val prefsRepo = mockk<PreferencesRepository>()
        val authRepo = mockk<AuthRepository>()
        val input = UserPreferences(preferred_audio_lang = "ja")
        val returned = UserPreferences(preferred_audio_lang = "ja", max_content_rating = "PG-13")
        coEvery { prefsRepo.set(input) } returns returned

        val vm = SettingsViewModel(prefsRepo, authRepo, scrobbleRepo())
        vm.savePreferences(input)
        // runCurrent (not advanceUntilIdle) runs the save body but does NOT advance
        // past the 2s scheduleSavedClear() delay, so the transient saved=true is observable.
        runCurrent()

        assertThat(vm.uiState.value.preferences).isEqualTo(returned)
        assertThat(vm.uiState.value.saved).isTrue()
    }

    @Test
    fun `clearSavedFlag only mutates when flag was set`() = runTest(dispatcher) {
        val prefsRepo = mockk<PreferencesRepository>()
        val authRepo = mockk<AuthRepository>()
        val input = UserPreferences()
        coEvery { prefsRepo.set(input) } returns input

        val vm = SettingsViewModel(prefsRepo, authRepo, scrobbleRepo())
        vm.clearSavedFlag()
        assertThat(vm.uiState.value.saved).isFalse()

        vm.savePreferences(input)
        // See savePreferences test: runCurrent keeps saved=true visible (no 2s clear).
        runCurrent()
        assertThat(vm.uiState.value.saved).isTrue()

        vm.clearSavedFlag()
        assertThat(vm.uiState.value.saved).isFalse()
    }

    @Test
    fun `linkListenBrainz trims token, enables, and reloads status`() = runTest(dispatcher) {
        val prefsRepo = mockk<PreferencesRepository>()
        val authRepo = mockk<AuthRepository>()
        val scrobble = mockk<ScrobbleRepository>()
        coEvery { scrobble.setListenBrainz(any(), any()) } returns Unit
        coEvery { scrobble.status() } returns
            ScrobbleStatusResponse(listenbrainz_linked = true, listenbrainz_enabled = true)

        val vm = SettingsViewModel(prefsRepo, authRepo, scrobble)
        vm.linkListenBrainz("  tok123  ")
        advanceUntilIdle()

        coVerify { scrobble.setListenBrainz("tok123", true) }
        assertThat(vm.uiState.value.scrobbleLinked).isTrue()
    }

    @Test
    fun `unlinkListenBrainz clears with an empty token`() = runTest(dispatcher) {
        val prefsRepo = mockk<PreferencesRepository>()
        val authRepo = mockk<AuthRepository>()
        val scrobble = mockk<ScrobbleRepository>()
        coEvery { scrobble.setListenBrainz(any(), any()) } returns Unit
        coEvery { scrobble.status() } returns ScrobbleStatusResponse()

        val vm = SettingsViewModel(prefsRepo, authRepo, scrobble)
        vm.unlinkListenBrainz()
        advanceUntilIdle()

        coVerify { scrobble.setListenBrainz("", false) }
        assertThat(vm.uiState.value.scrobbleLinked).isFalse()
    }
}
