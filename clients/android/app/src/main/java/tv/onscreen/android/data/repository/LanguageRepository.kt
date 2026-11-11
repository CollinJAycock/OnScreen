package tv.onscreen.android.data.repository

import android.content.Context
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CancellationException
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.i18n.AccountLanguageGuard
import tv.onscreen.android.i18n.AppLanguage
import tv.onscreen.android.i18n.AppLocale
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The account's interface language (users.ui_language, server v3): read with
 * every GET /users/me/preferences ([PreferencesRepository] hands it here) and
 * set with PUT /users/me/ui-language. Applied through [AppLocale].
 */
@Singleton
open class LanguageRepository @Inject constructor(
    private val api: OnScreenApi,
    @ApplicationContext private val context: Context,
) {
    private val guard = AccountLanguageGuard()

    /** This device's choice; null = the device's language. */
    open fun choice(): String? = AppLocale.choice(context)

    /** The language the app shows now. */
    open fun current(): String = AppLocale.current(context)

    /** Taken when a preferences read starts ([applyAccount]). */
    open fun readStarted(): Int = guard.readStarted()

    /**
     * A preferences read that started at [token] answered [uiLanguage]. The
     * account's choice, when it has one, wins over this device's and is
     * remembered here; none (null) leaves this device as it is. A read that
     * predates a pick made here is ignored ([AccountLanguageGuard]).
     */
    open fun applyAccount(token: Int, uiLanguage: String?) {
        val tag = AppLanguage.supported(uiLanguage) ?: return
        if (!guard.mayApply(token)) return
        if (tag == AppLocale.choice(context)) return
        AppLocale.setChoice(context, tag)
    }

    /**
     * Settings › Language: switch to [tag] (null = the device's language) at
     * once, then save it to the account. False when the save failed (a server
     * before v3 answers 404): the language stays on this device.
     */
    open suspend fun choose(tag: String?): Boolean {
        val next = AppLanguage.supported(tag)
        guard.changeStarted()
        try {
            AppLocale.setChoice(context, next, keepScreen = true)
            api.setUiLanguage(bodyFor(next).toRequestBody(JSON))
            return true
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            return false
        } finally {
            guard.changeSaved()
        }
    }

    companion object {
        private val JSON = "application/json; charset=utf-8".toMediaType()

        /** `{"ui_language":"es"}`, or `{"ui_language":null}` to clear it.
         *  [tag] is one of [AppLanguage.SUPPORTED] or null, so it needs no
         *  escaping. */
        fun bodyFor(tag: String?): String =
            "{\"ui_language\":" + (AppLanguage.supported(tag)?.let { "\"$it\"" } ?: "null") + "}"
    }
}
