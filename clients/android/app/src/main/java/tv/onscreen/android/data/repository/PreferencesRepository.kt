package tv.onscreen.android.data.repository

import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.QualityProfile
import tv.onscreen.android.data.model.QualityProfileBody
import tv.onscreen.android.data.model.UserPreferences
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
open class PreferencesRepository @Inject constructor(
    private val api: OnScreenApi,
    /** Takes the account's ui_language from every read (null in tests that
     *  don't care about it). */
    private val language: LanguageRepository? = null,
) {
    @Volatile
    private var cached: UserPreferences? = null

    /** The [LanguageRepository.readStarted] token of the read [cached] came
     *  from, while its ui_language is still to be applied. */
    @Volatile
    private var languageToken: Int? = null

    /**
     * The preferences, from the cache when there is one. The account's
     * interface language they carry is applied (it can recreate the screen
     * in another language) unless [applyLanguage] is false: the player reads
     * them too, and a switch mid-playback would restart it. That read's
     * language then applies at the next read that may.
     */
    open suspend fun get(applyLanguage: Boolean = true): UserPreferences {
        cached?.let { prefs ->
            if (applyLanguage) applyLanguage(prefs)
            return prefs
        }
        val token = language?.readStarted()
        val fresh = api.getPreferences().data
        cached = fresh
        languageToken = token
        if (applyLanguage) applyLanguage(fresh)
        return fresh
    }

    private fun applyLanguage(prefs: UserPreferences) {
        val token = languageToken ?: return
        languageToken = null
        language?.applyAccount(token, prefs.ui_language)
    }

    /** Force a server round-trip, replacing the cached snapshot. Settings
     *  uses this on open: the process-lifetime cache is fine for playback
     *  decisions, but an EDITING surface must start from the server's
     *  current state or its full-object PUT reverts changes made on other
     *  clients since this process first cached. */
    open suspend fun refresh(): UserPreferences {
        cached = null
        languageToken = null
        return get()
    }

    open suspend fun set(prefs: UserPreferences): UserPreferences {
        api.setPreferences(prefs)
        // Re-read rather than caching what we sent. The PUT deliberately
        // persists only a subset — hub_layout has its own endpoint, and
        // max_content_rating is the admin-set parental ceiling the client must
        // never author — so echoing the request body back into the cache would
        // have the app believe it changed values the server ignored.
        cached = null
        languageToken = null
        return get()
    }

    /**
     * Change the quality profile (v2.6). The endpoint replaces the whole
     * profile, so [change] is applied to the profile as a FRESH read holds
     * it — the fields it leaves alone (forced_subtitles_only among them) go
     * back as they are, never cleared by a stale or default value. Returns
     * the preferences re-read after the write. A refusal (400) throws the
     * HttpException.
     */
    open suspend fun setQualityProfile(change: (QualityProfileBody) -> QualityProfileBody): UserPreferences {
        val body = change(QualityProfile.fromPreferences(refresh()))
        api.setQualityProfile(
            QualityProfile.toJson(body).toRequestBody("application/json; charset=utf-8".toMediaType()),
        )
        return refresh()
    }

    /** Drop the cached preferences. MUST be called on every identity change:
     *  this is a @Singleton, so without it the cache lives for the whole
     *  process and the next user to sign in on a shared TV inherits the
     *  previous user's hub layout, audio/subtitle languages and rating labels —
     *  and saving any one setting writes the rest of that stale object onto
     *  their account. */
    fun invalidate() {
        cached = null
        languageToken = null
    }
}
