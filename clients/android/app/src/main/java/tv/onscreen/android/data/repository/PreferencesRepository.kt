package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
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
