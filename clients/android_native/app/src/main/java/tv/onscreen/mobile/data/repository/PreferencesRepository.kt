package tv.onscreen.mobile.data.repository

import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.QualityProfileRequest
import tv.onscreen.mobile.data.model.UiLanguageRequest
import tv.onscreen.mobile.data.model.UserPreferences
import tv.onscreen.mobile.i18n.LanguageController
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
open class PreferencesRepository @Inject constructor(
    private val api: OnScreenApi,
    private val language: LanguageController,
) {
    @Volatile
    private var cached: UserPreferences? = null

    open suspend fun get(): UserPreferences {
        cached?.let { return it }
        // Every read from the server applies the account's interface
        // language (ui_language), so a choice made on another device arrives
        // with the hub's first load after sign-in and when Settings opens.
        val applyLanguage = language.accountReader()
        val fresh = api.getPreferences().data
        cached = fresh
        applyLanguage(fresh.ui_language)
        return fresh
    }

    open suspend fun set(prefs: UserPreferences): UserPreferences {
        val updated = api.setPreferences(prefs).data
        cached = updated
        return updated
    }

    fun invalidate() {
        cached = null
    }

    /** A read straight from the server, never the cache — what a save that
     *  echoes the fields it doesn't change must start from. */
    open suspend fun fresh(): UserPreferences {
        invalidate()
        return get()
    }

    /** Save the account's interface language (PUT /users/me/ui-language;
     *  null = no choice, each device follows its own). Throws when the save
     *  fails: a 404 from a server before v3. The cached preferences carry
     *  the language, so they go too. */
    open suspend fun setUiLanguage(tag: String?) {
        try {
            api.setUiLanguage(UiLanguageRequest(tag))
        } finally {
            invalidate()
        }
    }

    /** Replace the quality profile (see QualityProfile.request). The cached
     *  preferences carry it, so they go too. */
    open suspend fun setQualityProfile(body: QualityProfileRequest) {
        try {
            api.setQualityProfile(body)
        } finally {
            invalidate()
        }
    }
}
