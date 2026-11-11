package tv.onscreen.android.data.repository

import android.content.Context
import com.google.common.truth.Truth.assertThat
import io.mockk.Runs
import io.mockk.coEvery
import io.mockk.coVerifyOrder
import io.mockk.every
import io.mockk.just
import io.mockk.mockk
import io.mockk.mockkObject
import io.mockk.slot
import io.mockk.unmockkObject
import io.mockk.verify
import kotlinx.coroutines.test.runTest
import okhttp3.RequestBody
import okhttp3.ResponseBody.Companion.toResponseBody
import okio.Buffer
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.i18n.AppLocale

/** Settings › Language and the account's ui_language: switch here first,
 *  then save; the account's choice applies from a read unless this TV
 *  changed it meanwhile. AppLocale (the Android side) is stubbed. */
class LanguageRepositoryTest {

    private val context = mockk<Context>(relaxed = true)
    private var choice: String? = null

    @Before
    fun setUp() {
        mockkObject(AppLocale)
        every { AppLocale.choice(any()) } answers { choice }
        every { AppLocale.setChoice(any(), any(), any()) } answers { choice = secondArg() }
    }

    @After
    fun tearDown() {
        unmockkObject(AppLocale)
    }

    private fun RequestBody.text(): String = Buffer().also { writeTo(it) }.readUtf8()

    @Test
    fun `the body names the language, or null to follow the device`() {
        assertThat(LanguageRepository.bodyFor("es")).isEqualTo("""{"ui_language":"es"}""")
        assertThat(LanguageRepository.bodyFor("zh-hans")).isEqualTo("""{"ui_language":"zh-Hans"}""")
        assertThat(LanguageRepository.bodyFor(null)).isEqualTo("""{"ui_language":null}""")
        assertThat(LanguageRepository.bodyFor("fr")).isEqualTo("""{"ui_language":null}""")
    }

    @Test
    fun `a pick switches this TV first, then saves it to the account`() = runTest {
        val api = mockk<OnScreenApi>()
        val body = slot<RequestBody>()
        coEvery { api.setUiLanguage(capture(body)) } just Runs
        val repo = LanguageRepository(api, context)

        assertThat(repo.choose("es")).isTrue()

        coVerifyOrder {
            AppLocale.setChoice(context, "es", true)
            api.setUiLanguage(any())
        }
        assertThat(body.captured.text()).isEqualTo("""{"ui_language":"es"}""")
        assertThat(choice).isEqualTo("es")
    }

    @Test
    fun `a server before v3 (404) keeps the language on this TV and says the save failed`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.setUiLanguage(any()) } throws
            HttpException(Response.error<Unit>(404, "".toResponseBody(null)))
        val repo = LanguageRepository(api, context)

        assertThat(repo.choose("zh-Hans")).isFalse()
        assertThat(choice).isEqualTo("zh-Hans")
    }

    @Test
    fun `the account's language applies and is remembered here`() {
        val repo = LanguageRepository(mockk(), context)

        repo.applyAccount(repo.readStarted(), "es")
        assertThat(choice).isEqualTo("es")

        // The same again, and no choice on the account: nothing changes.
        repo.applyAccount(repo.readStarted(), "es")
        repo.applyAccount(repo.readStarted(), null)
        repo.applyAccount(repo.readStarted(), "fr")
        verify(exactly = 1) { AppLocale.setChoice(any(), any(), any()) }
        assertThat(choice).isEqualTo("es")
    }

    @Test
    fun `a read that started before a pick on this TV doesn't undo it`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.setUiLanguage(any()) } just Runs
        val repo = LanguageRepository(api, context)

        val staleRead = repo.readStarted()
        repo.choose("es")
        repo.applyAccount(staleRead, "zh-Hans")

        assertThat(choice).isEqualTo("es")
    }
}
