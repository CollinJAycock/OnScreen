package tv.onscreen.mobile.data.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

private val Context.subtitleStore: DataStore<Preferences> by preferencesDataStore(
    name = "subtitle_prefs",
)

/**
 * DataStore-backed persistence for [SubtitleStyle]. Separate file from
 * `server_prefs` because subtitle styling is a settings concern, not an
 * auth concern — and the user expectation is that signing out doesn't
 * reset their subtitle preferences.
 *
 * Per-field keys (instead of one JSON blob) so a future schema addition
 * — say `position` or `letter_spacing` — doesn't have to deserialise the
 * whole shape forwards-and-back.
 */
@Singleton
class SubtitlePrefs @Inject constructor(
    @ApplicationContext private val context: Context,
) {

    companion object {
        private val KEY_SIZE = stringPreferencesKey("subtitle_size")
        private val KEY_COLOR = stringPreferencesKey("subtitle_color")
        private val KEY_BG = stringPreferencesKey("subtitle_background")
        private val KEY_OUTLINE = stringPreferencesKey("subtitle_outline")
    }

    val style: Flow<SubtitleStyle> = context.subtitleStore.data.map { p ->
        SubtitleStyle(
            size = SubtitleStyle.parseSize(p[KEY_SIZE]),
            color = SubtitleStyle.parseColor(p[KEY_COLOR]),
            background = SubtitleStyle.parseBackground(p[KEY_BG]),
            outline = SubtitleStyle.parseOutline(p[KEY_OUTLINE]),
        )
    }

    suspend fun setSize(s: SubtitleStyle.Size) {
        context.subtitleStore.edit { it[KEY_SIZE] = SubtitleStyle.serializeSize(s) }
    }

    suspend fun setColor(c: SubtitleStyle.TextColor) {
        context.subtitleStore.edit { it[KEY_COLOR] = SubtitleStyle.serializeColor(c) }
    }

    suspend fun setBackground(b: SubtitleStyle.Background) {
        context.subtitleStore.edit { it[KEY_BG] = SubtitleStyle.serializeBackground(b) }
    }

    suspend fun setOutline(o: SubtitleStyle.Outline) {
        context.subtitleStore.edit { it[KEY_OUTLINE] = SubtitleStyle.serializeOutline(o) }
    }
}
