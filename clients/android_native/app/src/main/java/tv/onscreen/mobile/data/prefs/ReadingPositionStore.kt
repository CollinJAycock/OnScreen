package tv.onscreen.mobile.data.prefs

import android.content.Context
import dagger.hilt.android.qualifiers.ApplicationContext
import tv.onscreen.mobile.data.model.ReadingPosition
import javax.inject.Inject
import javax.inject.Singleton

/**
 * This device's copy of where the user is in each book (v2.6 reading sync):
 * written at every page turn, so a position whose server save didn't land
 * (offline, the reader closed mid-debounce and the request failed) still
 * opens next time — the reader takes the newer of this and the server's.
 *
 * Per identity: AuthRepository clears it on every sign-in / sign-out, as it
 * does the other per-user caches.
 */
@Singleton
open class ReadingPositionStore @Inject constructor(
    @ApplicationContext private val context: Context,
) {
    private val prefs by lazy { context.getSharedPreferences("reading_positions", Context.MODE_PRIVATE) }

    open fun get(itemId: String): ReadingPosition? {
        val locator = prefs.getString("$itemId.locator", null) ?: return null
        return ReadingPosition(
            locator = locator,
            progression = prefs.getFloat("$itemId.progression", 0f).toDouble(),
            updatedAtMs = prefs.getLong("$itemId.at", 0L),
        )
    }

    open fun put(itemId: String, position: ReadingPosition) {
        prefs.edit()
            .putString("$itemId.locator", position.locator)
            .putFloat("$itemId.progression", position.progression.toFloat())
            .putLong("$itemId.at", position.updatedAtMs)
            .apply()
    }

    open fun clear() {
        prefs.edit().clear().apply()
    }
}
