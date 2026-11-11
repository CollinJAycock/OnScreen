package tv.onscreen.mobile.i18n

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.test.runTest
import org.junit.Test
import java.util.Locale

class AppLanguageTest {

    private fun match(tag: String) = AppLanguage.match(Locale.forLanguageTag(tag))

    @Test
    fun `ships English, Spanish and Simplified Chinese`() {
        assertThat(AppLanguage.TAGS).containsExactly("en", "es", "zh-Hans").inOrder()
        assertThat(AppLanguage.isSupported("es")).isTrue()
        assertThat(AppLanguage.isSupported("fr")).isFalse()
        assertThat(AppLanguage.isSupported("zh")).isFalse()
        assertThat(AppLanguage.isSupported(null)).isFalse()
    }

    @Test
    fun `any English or Spanish locale matches`() {
        assertThat(match("en-GB")).isEqualTo("en")
        assertThat(match("es")).isEqualTo("es")
        assertThat(match("es-419")).isEqualTo("es")
        assertThat(match("es-MX")).isEqualTo("es")
    }

    @Test
    fun `Simplified Chinese matches its regions and script, as the web app does`() {
        assertThat(match("zh")).isEqualTo("zh-Hans")
        assertThat(match("zh-CN")).isEqualTo("zh-Hans")
        assertThat(match("zh-SG")).isEqualTo("zh-Hans")
        assertThat(match("zh-MY")).isEqualTo("zh-Hans")
        assertThat(match("zh-Hans")).isEqualTo("zh-Hans")
        assertThat(match("zh-Hans-HK")).isEqualTo("zh-Hans")
    }

    @Test
    fun `Traditional Chinese and other languages don't match`() {
        assertThat(match("zh-TW")).isNull()
        assertThat(match("zh-HK")).isNull()
        assertThat(match("zh-Hant")).isNull()
        assertThat(match("zh-Hant-CN")).isNull()
        assertThat(match("fr-FR")).isNull()
        assertThat(match("ja")).isNull()
    }
}

/** A store like the platform's: the device's choice, null = follow the device. */
private class FakeStore(var value: String? = null) : LanguageController.Store {
    val writes = mutableListOf<String?>()
    override fun get(): String? = value
    override fun set(tag: String?) {
        writes += tag
        value = tag
    }
}

class LanguageControllerTest {

    @Test
    fun `with no choice the app follows the device`() {
        val c = LanguageController(FakeStore())
        assertThat(c.choice()).isNull()
    }

    @Test
    fun `the device's choice is used, a stale unsupported value is not`() {
        assertThat(LanguageController(FakeStore("es")).choice()).isEqualTo("es")
        assertThat(LanguageController(FakeStore("fr")).choice()).isNull()
    }

    @Test
    fun `the account's language wins over the device's and is remembered`() {
        val store = FakeStore("es")
        val c = LanguageController(store)
        c.applyAccount("zh-Hans")
        assertThat(c.choice()).isEqualTo("zh-Hans")
        assertThat(store.writes).containsExactly("zh-Hans")
    }

    @Test
    fun `an account with no language keeps the device's choice`() {
        val store = FakeStore("es")
        val c = LanguageController(store)
        c.applyAccount(null)
        assertThat(c.choice()).isEqualTo("es")
        assertThat(store.writes).isEmpty()

        val following = FakeStore(null)
        LanguageController(following).applyAccount(null)
        assertThat(following.value).isNull()
        assertThat(following.writes).isEmpty()
    }

    @Test
    fun `an account language this app doesn't ship is ignored`() {
        val store = FakeStore("es")
        LanguageController(store).applyAccount("fr")
        assertThat(store.value).isEqualTo("es")
        assertThat(store.writes).isEmpty()
    }

    @Test
    fun `the same language isn't written again`() {
        val store = FakeStore("es")
        LanguageController(store).applyAccount("es")
        assertThat(store.writes).isEmpty()
    }

    @Test
    fun `a preferences read that started before a change here is ignored`() = runTest {
        val store = FakeStore("en")
        val c = LanguageController(store)
        val reader = c.accountReader()
        c.choose("es") { }
        reader("en") // the old answer arrives after the pick
        assertThat(c.choice()).isEqualTo("es")
    }

    @Test
    fun `a read that started after the change applies`() = runTest {
        val store = FakeStore("en")
        val c = LanguageController(store)
        c.choose("es") { }
        c.accountReader()("zh-Hans")
        assertThat(c.choice()).isEqualTo("zh-Hans")
    }

    @Test
    fun `a read that started while the save was on its way is ignored`() = runTest {
        val store = FakeStore("en")
        val c = LanguageController(store)
        val saving = CompletableDeferred<Unit>()
        val pick = async { c.choose("es") { saving.await() } }
        testScheduler.runCurrent()
        val reader = c.accountReader() // starts mid-save
        saving.complete(Unit)
        assertThat(pick.await()).isTrue()
        reader("en")
        assertThat(c.choice()).isEqualTo("es")
    }

    @Test
    fun `picking switches at once and saves to the account`() = runTest {
        val store = FakeStore(null)
        val c = LanguageController(store)
        var saved: String? = "unset"
        val ok = c.choose("zh-Hans") {
            assertThat(store.value).isEqualTo("zh-Hans") // switched before the save
            saved = it
        }
        assertThat(ok).isTrue()
        assertThat(saved).isEqualTo("zh-Hans")
    }

    @Test
    fun `device language clears the choice and saves null`() = runTest {
        val store = FakeStore("es")
        val c = LanguageController(store)
        var saved: String? = "unset"
        assertThat(c.choose(null) { saved = it }).isTrue()
        assertThat(c.choice()).isNull()
        assertThat(saved).isNull()
    }

    @Test
    fun `a failed save keeps the language on this device`() = runTest {
        val store = FakeStore("en")
        val c = LanguageController(store)
        val ok = c.choose("es") { throw RuntimeException("HTTP 404") }
        assertThat(ok).isFalse()
        assertThat(c.choice()).isEqualTo("es")
    }
}
