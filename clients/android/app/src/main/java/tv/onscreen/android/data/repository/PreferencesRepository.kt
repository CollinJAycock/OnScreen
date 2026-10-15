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
) {
    @Volatile
    private var cached: UserPreferences? = null

    open suspend fun get(): UserPreferences {
        cached?.let { return it }
        val fresh = api.getPreferences().data
        cached = fresh
        return fresh
    }

    /** Force a server round-trip, replacing the cached snapshot. Settings
     *  uses this on open: the process-lifetime cache is fine for playback
     *  decisions, but an EDITING surface must start from the server's
     *  current state or its full-object PUT reverts changes made on other
     *  clients since this process first cached. */
    open suspend fun refresh(): UserPreferences {
        cached = null
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
    }
}
