package tv.onscreen.android.i18n

/**
 * The interface languages the app ships, and which one to show. Pure, so the
 * rules are JVM-tested (AppLanguageTest); [AppLocale] applies the answer.
 *
 * Same order as the web and TV web apps (docs/i18n.md):
 *  1. the account's `ui_language` (GET /users/me/preferences);
 *  2. else the language chosen on this device;
 *  3. else the device's own language;
 *  4. else English.
 *
 * The account's choice is remembered on the device when it arrives
 * ([AppLocale.setChoice]), so before sign-in and on the next start it is the
 * device's choice.
 */
object AppLanguage {
    const val ENGLISH = "en"
    const val SPANISH = "es"
    const val CHINESE = "zh-Hans"

    /** The tags as the server stores them (users.ui_language CHECK), in the
     *  picker's order. */
    val SUPPORTED: List<String> = listOf(ENGLISH, SPANISH, CHINESE)

    /** Each language named in itself, for the picker. Never translated. */
    val NAMES: Map<String, String> = linkedMapOf(
        ENGLISH to "English",
        SPANISH to "Español",
        CHINESE to "简体中文",
    )

    /** [tag] when it is one of the shipped tags (any case), else null. */
    fun supported(tag: String?): String? {
        val t = tag?.trim()
        if (t.isNullOrEmpty()) return null
        return SUPPORTED.firstOrNull { it.equals(t, ignoreCase = true) }
    }

    /**
     * The best shipped language for BCP 47 [tags] in preference order (the
     * device's languages), or null. English, Spanish of any region, and
     * Simplified Chinese (`zh`, `zh-CN`, `zh-SG`, `zh-MY`, `zh-Hans-*`).
     * Traditional Chinese (zh-TW, zh-HK, zh-Hant) isn't shipped, so it
     * doesn't match: the next tag decides. Mirrors matchLocale in the web
     * app's i18n module.
     */
    fun match(tags: List<String>): String? {
        for (raw in tags) {
            val parts = raw.trim().replace('_', '-').lowercase().split('-')
            when (parts.firstOrNull()) {
                "en" -> return ENGLISH
                "es" -> return SPANISH
                "zh" -> {
                    val second = parts.getOrNull(1)
                    if (second == null || second == "hans" || second == "cn" || second == "sg" || second == "my") {
                        return CHINESE
                    }
                }
            }
        }
        return null
    }

    /** The language to show: the account's, else this device's choice, else
     *  the device's languages ([system]), else English. */
    fun resolve(account: String?, device: String?, system: List<String>): String =
        supported(account) ?: supported(device) ?: match(system) ?: ENGLISH
}

/**
 * Keeps a preferences read from putting back a language this device has just
 * changed. A read that was already on its way when the language was picked
 * here, or that started while the pick was still being saved to the account,
 * answers with what the server had before: it is ignored. Same rule as the
 * TV web apps' accountLocaleReader.
 */
class AccountLanguageGuard {
    private var changes = 0
    private var saving = 0

    /** Taken when a preferences read starts; hand it back to [mayApply]. */
    @Synchronized
    fun readStarted(): Int = if (saving > 0) STALE else changes

    /** Whether the read that took [token] may apply its ui_language. */
    @Synchronized
    fun mayApply(token: Int): Boolean = token != STALE && token == changes && saving == 0

    /** The language was picked on this device and its save started. */
    @Synchronized
    fun changeStarted() {
        changes++
        saving++
    }

    /** That save finished, either way. */
    @Synchronized
    fun changeSaved() {
        if (saving > 0) saving--
    }

    private companion object {
        const val STALE = -1
    }
}
