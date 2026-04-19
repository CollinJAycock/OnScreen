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
        val updated = api.setPreferences(prefs).data
        cached = updated
        return updated
    }

    fun invalidate() {
        cached = null
    }
}
