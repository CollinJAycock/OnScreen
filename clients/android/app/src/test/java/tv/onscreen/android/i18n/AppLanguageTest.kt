package tv.onscreen.android.i18n

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Which interface language the app shows (the same order as the web and TV
 *  web apps), and the guard that keeps a stale preferences read from undoing
 *  a pick made on this TV. */
class AppLanguageTest {

    @Test
    fun `the account's choice wins, then this device's, then the device's languages, then English`() {
        assertThat(AppLanguage.resolve(account = "es", device = "zh-Hans", system = listOf("en-US"))).isEqualTo("es")
        assertThat(AppLanguage.resolve(account = null, device = "zh-Hans", system = listOf("es-ES"))).isEqualTo("zh-Hans")
        assertThat(AppLanguage.resolve(account = null, device = null, system = listOf("es-MX"))).isEqualTo("es")
        assertThat(AppLanguage.resolve(account = null, device = null, system = listOf("fr-FR"))).isEqualTo("en")
        assertThat(AppLanguage.resolve(account = null, device = null, system = emptyList())).isEqualTo("en")
        // An unknown or empty value at any step is no choice.
        assertThat(AppLanguage.resolve(account = "fr", device = "", system = listOf("zh-CN"))).isEqualTo("zh-Hans")
    }

    @Test
    fun `device languages match in preference order`() {
        assertThat(AppLanguage.match(listOf("es-419"))).isEqualTo("es")
        assertThat(AppLanguage.match(listOf("en-GB", "es-ES"))).isEqualTo("en")
        assertThat(AppLanguage.match(listOf("fr-FR", "es-ES"))).isEqualTo("es")
        assertThat(AppLanguage.match(listOf("de", "it"))).isNull()
        assertThat(AppLanguage.match(listOf("es_US"))).isEqualTo("es")
    }

    @Test
    fun `Simplified Chinese matches, Traditional doesn't`() {
        for (tag in listOf("zh", "zh-CN", "zh-SG", "zh-MY", "zh-Hans", "zh-Hans-CN", "ZH-hans")) {
            assertThat(AppLanguage.match(listOf(tag))).isEqualTo("zh-Hans")
        }
        for (tag in listOf("zh-TW", "zh-HK", "zh-Hant", "zh-Hant-TW")) {
            assertThat(AppLanguage.match(listOf(tag))).isNull()
        }
        // The next language decides.
        assertThat(AppLanguage.match(listOf("zh-TW", "en-US"))).isEqualTo("en")
    }

    @Test
    fun `only the shipped tags are a choice`() {
        assertThat(AppLanguage.supported("es")).isEqualTo("es")
        assertThat(AppLanguage.supported("ZH-HANS")).isEqualTo("zh-Hans")
        assertThat(AppLanguage.supported(" en ")).isEqualTo("en")
        for (other in listOf(null, "", "es-ES", "zh", "fr")) {
            assertThat(AppLanguage.supported(other)).isNull()
        }
        assertThat(AppLanguage.SUPPORTED).containsExactly("en", "es", "zh-Hans").inOrder()
        assertThat(AppLanguage.NAMES.values).containsExactly("English", "Español", "简体中文").inOrder()
    }

    @Test
    fun `a read that started before a pick, or during its save, is ignored`() {
        val guard = AccountLanguageGuard()
        val before = guard.readStarted()
        assertThat(guard.mayApply(before)).isTrue()

        guard.changeStarted() // picked on this TV, save on its way
        assertThat(guard.mayApply(before)).isFalse()
        val during = guard.readStarted()
        guard.changeSaved()
        assertThat(guard.mayApply(during)).isFalse()

        // A read that starts after the save finished applies again.
        val after = guard.readStarted()
        assertThat(guard.mayApply(after)).isTrue()
    }

    @Test
    fun `an unmatched save end doesn't open the guard early`() {
        val guard = AccountLanguageGuard()
        guard.changeSaved()
        guard.changeStarted()
        guard.changeStarted()
        guard.changeSaved()
        assertThat(guard.mayApply(guard.readStarted())).isFalse()
        guard.changeSaved()
        assertThat(guard.mayApply(guard.readStarted())).isTrue()
    }
}
