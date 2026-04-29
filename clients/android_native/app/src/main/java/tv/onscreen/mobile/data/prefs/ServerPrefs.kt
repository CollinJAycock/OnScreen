package tv.onscreen.mobile.data.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map

private val Context.dataStore: DataStore<Preferences> by preferencesDataStore(name = "server_prefs")

class ServerPrefs(private val context: Context) {

    companion object {
        private val KEY_SERVER_URL = stringPreferencesKey("server_url")
        private val KEY_ACCESS_TOKEN = stringPreferencesKey("access_token")
        private val KEY_REFRESH_TOKEN = stringPreferencesKey("refresh_token")
        private val KEY_USER_ID = stringPreferencesKey("user_id")
        private val KEY_USERNAME = stringPreferencesKey("username")
        // Search type-filter checkboxes — mirror the web /search
        // page's localStorage('onscreen_search_filters'). Defaults
        // (movie + show on, episode + track off) match the web
        // defaults so the user gets the same first-search shape on
        // either client.
        private val KEY_FILTER_MOVIE = booleanPreferencesKey("search_filter_movie")
        private val KEY_FILTER_SHOW = booleanPreferencesKey("search_filter_show")
        private val KEY_FILTER_EPISODE = booleanPreferencesKey("search_filter_episode")
        private val KEY_FILTER_TRACK = booleanPreferencesKey("search_filter_track")
    }

    val serverUrl: Flow<String?> = context.dataStore.data.map { it[KEY_SERVER_URL] }
    val accessToken: Flow<String?> = context.dataStore.data.map { it[KEY_ACCESS_TOKEN] }
    val refreshToken: Flow<String?> = context.dataStore.data.map { it[KEY_REFRESH_TOKEN] }
    val userId: Flow<String?> = context.dataStore.data.map { it[KEY_USER_ID] }
    val username: Flow<String?> = context.dataStore.data.map { it[KEY_USERNAME] }

    val isLoggedIn: Flow<Boolean> = context.dataStore.data.map {
        !it[KEY_ACCESS_TOKEN].isNullOrEmpty()
    }

    val hasServer: Flow<Boolean> = context.dataStore.data.map {
        !it[KEY_SERVER_URL].isNullOrEmpty()
    }

    suspend fun getServerUrl(): String? = serverUrl.first()
    suspend fun getAccessToken(): String? = accessToken.first()
    suspend fun getRefreshToken(): String? = refreshToken.first()

    suspend fun setServerUrl(url: String) {
        context.dataStore.edit { it[KEY_SERVER_URL] = url.trimEnd('/') }
    }

    suspend fun setTokens(accessToken: String, refreshToken: String) {
        context.dataStore.edit {
            it[KEY_ACCESS_TOKEN] = accessToken
            it[KEY_REFRESH_TOKEN] = refreshToken
        }
    }

    suspend fun setUser(userId: String, username: String) {
        context.dataStore.edit {
            it[KEY_USER_ID] = userId
            it[KEY_USERNAME] = username
        }
    }

    suspend fun clearAuth() {
        context.dataStore.edit {
            it.remove(KEY_ACCESS_TOKEN)
            it.remove(KEY_REFRESH_TOKEN)
            it.remove(KEY_USER_ID)
            it.remove(KEY_USERNAME)
        }
    }

    suspend fun clearAll() {
        context.dataStore.edit { it.clear() }
    }

    // ── Search filters ──────────────────────────────────────────────────────

    /** Reactive filter state for the search screen. UI binds to this so
     *  toggling a chip immediately re-filters the visible result rows. */
    val searchFilters: Flow<SearchFilters> = context.dataStore.data.map {
        SearchFilters(
            movie = it[KEY_FILTER_MOVIE] ?: true,
            show = it[KEY_FILTER_SHOW] ?: true,
            episode = it[KEY_FILTER_EPISODE] ?: false,
            track = it[KEY_FILTER_TRACK] ?: false,
        )
    }

    suspend fun setSearchFilters(filters: SearchFilters) {
        context.dataStore.edit {
            it[KEY_FILTER_MOVIE] = filters.movie
            it[KEY_FILTER_SHOW] = filters.show
            it[KEY_FILTER_EPISODE] = filters.episode
            it[KEY_FILTER_TRACK] = filters.track
        }
    }
}

/** Type-filter checkboxes shown above the search results. The four
 *  visible toggles cover the headline media types; album / artist /
 *  season piggyback on existing filters server-side (handled in
 *  SearchViewModel.applyFilters) so we don't need eight checkboxes for
 *  what's effectively four mental categories. */
data class SearchFilters(
    val movie: Boolean,
    val show: Boolean,
    val episode: Boolean,
    val track: Boolean,
)
