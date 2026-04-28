package tv.onscreen.android.ui.settings

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.android.data.model.UserPreferences
import tv.onscreen.android.data.repository.AuthRepository
import tv.onscreen.android.data.repository.PreferencesRepository

@OptIn(ExperimentalCoroutinesApi::class)
class SettingsViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() { Dispatchers.setMain(dispatcher) }

    @After
    fun tearDown() { Dispatchers.resetMain() }

    @Test
    fun `load fetches preferences and stores identity`() = runTest(dispatcher) {
        val prefsRepo = mockk<PreferencesRepository>()
        val authRepo = mockk<AuthRepository>()
        val saved = UserPreferences(preferred_audio_lang = "en")
        coEvery { prefsRepo.get() } returns saved

        val vm = SettingsViewModel(prefsRepo, authRepo)
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
    fun `load records error when preferences fetch fails`() = runTest(dispatcher) {
        val prefsRepo = mockk<PreferencesRepository>()
        val authRepo = mockk<AuthRepository>()
        coEvery { prefsRepo.get() } throws RuntimeException("boom")

        val vm = SettingsViewModel(prefsRepo, authRepo)
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

        val vm = SettingsViewModel(prefsRepo, authRepo)
        vm.savePreferences(input)
        advanceUntilIdle()

        assertThat(vm.uiState.value.preferences).isEqualTo(returned)
        assertThat(vm.uiState.value.saved).isTrue()
    }

    @Test
    fun `clearSavedFlag only mutates when flag was set`() = runTest(dispatcher) {
        val prefsRepo = mockk<PreferencesRepository>()
        val authRepo = mockk<AuthRepository>()
        val input = UserPreferences()
        coEvery { prefsRepo.set(input) } returns input

        val vm = SettingsViewModel(prefsRepo, authRepo)
        vm.clearSavedFlag()
        assertThat(vm.uiState.value.saved).isFalse()

        vm.savePreferences(input)
        advanceUntilIdle()
        assertThat(vm.uiState.value.saved).isTrue()

        vm.clearSavedFlag()
        assertThat(vm.uiState.value.saved).isFalse()
    }
}
