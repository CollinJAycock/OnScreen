package tv.onscreen.mobile.i18n

import kotlinx.coroutines.CancellationException
import java.util.Locale
import java.util.concurrent.atomic.AtomicInteger

/**
 * The interface languages the app ships, as BCP 47 tags. Mirrors the
 * server's SupportedUILanguages (users.ui_language) and the web and TV apps.
 * Resources: values/ (English), values-es/, values-b+zh+Hans/.
 */
object AppLanguage {
    const val ENGLISH = "en"
    const val SPANISH = "es"
    const val CHINESE_SIMPLIFIED = "zh-Hans"

    val TAGS: List<String> = listOf(ENGLISH, SPANISH, CHINESE_SIMPLIFIED)

    fun isSupported(tag: String?): Boolean = tag != null && tag in TAGS

    /**
     * A locale as a shipped language, or null. Like the web app's
     * matchLocale: any `es` is Spanish; `zh` with no region or script, `zh-CN`,
     * `zh-SG`, `zh-MY` and `zh-Hans*` are Simplified Chinese. Traditional
     * Chinese (`zh-TW`, `zh-HK`, `zh-Hant`) isn't shipped.
     */
    fun match(locale: Locale): String? = when (locale.language.lowercase(Locale.ROOT)) {
        "en" -> ENGLISH
        "es" -> SPANISH
        "zh" -> when {
            locale.script.equals("Hans", ignoreCase = true) -> CHINESE_SIMPLIFIED
            locale.script.isNotEmpty() -> null
            locale.country.uppercase(Locale.ROOT) in SIMPLIFIED_REGIONS -> CHINESE_SIMPLIFIED
            else -> null
        }
        else -> null
    }

    private val SIMPLIFIED_REGIONS = setOf("", "CN", "SG", "MY")
}

/**
 * Which language the app shows, in the order the other apps use:
 *
 *  1. the account's choice (`ui_language` in GET /users/me/preferences), which
 *     is then remembered on this device ([applyAccount]);
 *  2. else this device's choice ([choice], set by the Settings picker);
 *  3. else the device's languages, and 4. else English: with no choice the
 *     app follows the device, and Android picks values-es/, values-b+zh+Hans/
 *     or the English default.
 *
 * [store] keeps the choice: the platform's per-app language on Android 13+,
 * a private preference before that (see [AppLocales]).
 */
class LanguageController(private val store: Store) {

    interface Store {
        /** This device's choice: a tag, or null to follow the device. */
        fun get(): String?

        fun set(tag: String?)
    }

    private val changes = AtomicInteger()

    /** This device's choice, or null when the app follows the device. */
    fun choice(): String? = store.get()?.takeIf(AppLanguage::isSupported)

    /**
     * Taken when a preferences read starts; call the result with the
     * `ui_language` it answered. A read already on its way when the language
     * was changed here is ignored: its answer predates the change and would
     * put the old language back.
     */
    fun accountReader(): (String?) -> Unit {
        val at = changes.get()
        return { uiLanguage -> if (at == changes.get()) applyAccount(uiLanguage) }
    }

    /** The account's choice wins and is remembered on this device. null (or
     *  a language this app doesn't ship) keeps whatever the device uses. */
    fun applyAccount(uiLanguage: String?) {
        if (AppLanguage.isSupported(uiLanguage) && uiLanguage != choice()) store.set(uiLanguage)
    }

    /**
     * The Settings picker: switch to [tag] (null = the device's language) at
     * once, then save it to the account. False when the save failed: the
     * language stays on this device and the screen says so.
     */
    suspend fun choose(tag: String?, save: suspend (String?) -> Unit): Boolean {
        changes.incrementAndGet()
        val next = tag?.takeIf(AppLanguage::isSupported)
        if (next != choice()) store.set(next)
        return try {
            save(next)
            true
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            false
        } finally {
            // A read that started while the save was on its way may still
            // have answered with the account's old language: ignore it too.
            changes.incrementAndGet()
        }
    }
}
