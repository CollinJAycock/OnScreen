package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.api.ApiResponse
import tv.onscreen.mobile.data.model.UiLanguageRequest
import tv.onscreen.mobile.data.model.UserPreferences
import tv.onscreen.mobile.i18n.LanguageController

/** The account's interface language arrives with the preferences. */
class PreferencesRepositoryLanguageTest {

    private val store = object : LanguageController.Store {
        var value: String? = null
        var writes = 0
        override fun get(): String? = value
        override fun set(tag: String?) {
            writes++
            value = tag
        }
    }
    private val language = LanguageController(store)
    private val api = mockk<OnScreenApi>(relaxed = true)
    private val repo = PreferencesRepository(api, language)

    @Test
    fun `a read from the server applies the account's language`() = runTest {
        coEvery { api.getPreferences() } returns ApiResponse(UserPreferences(ui_language = "es"))
        repo.get()
        assertThat(language.choice()).isEqualTo("es")
    }

    @Test
    fun `a cached read doesn't apply it again`() = runTest {
        coEvery { api.getPreferences() } returns ApiResponse(UserPreferences(ui_language = "es"))
        repo.get()
        store.value = "en" // changed here since
        repo.get()
        assertThat(language.choice()).isEqualTo("en")
        coVerify(exactly = 1) { api.getPreferences() }
    }

    @Test
    fun `an account with no language, or an older server, leaves the device's choice`() = runTest {
        store.value = "zh-Hans"
        coEvery { api.getPreferences() } returns ApiResponse(UserPreferences())
        repo.fresh()
        assertThat(language.choice()).isEqualTo("zh-Hans")
        assertThat(store.writes).isEqualTo(0)
    }

    @Test
    fun `saving the language sends it and drops the cached preferences`() = runTest {
        coEvery { api.getPreferences() } returns ApiResponse(UserPreferences())
        repo.get()
        repo.setUiLanguage("es")
        coVerify { api.setUiLanguage(UiLanguageRequest("es")) }
        repo.get()
        coVerify(exactly = 2) { api.getPreferences() }
    }

    @Test
    fun `device language saves null`() = runTest {
        repo.setUiLanguage(null)
        coVerify { api.setUiLanguage(UiLanguageRequest(null)) }
    }
}
