package tv.onscreen.android.data.prefs

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

    // Token values are encrypted at rest under an AndroidKeyStore key; see
    // TokenCrypto. Legacy plaintext values are migrated transparently on read.
    private val crypto = TokenCrypto()

    companion object {
        private val KEY_SERVER_URL = stringPreferencesKey("server_url")
        private val KEY_ACCESS_TOKEN = stringPreferencesKey("access_token")
        private val KEY_REFRESH_TOKEN = stringPreferencesKey("refresh_token")
        // purpose=asset token. Carried in `?token=` on asset URLs that
        // ExoPlayer / the media session can't attach a Bearer header
        // to. The server rejects the general access token in a URL.
        private val KEY_ASSET_TOKEN = stringPreferencesKey("asset_token")
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

        /**
         * Pick a scheme for a bare-host URL the user typed into the server
         * field. Ported from the phone client's normalizeServerUrl
         * (clients/android_native/.../ui/pair/PairViewModel.kt) — keep in step.
         *
         * Hosts that look like RFC1918 / loopback / `.local` / `localhost`
         * default to `http://`, because the typical HomeLab box isn't running
         * TLS. Everything else defaults to `https://`, because a public DNS
         * name almost always has a cert (Cloudflare Tunnel, Tailscale Funnel,
         * Let's Encrypt).
         *
         * This used to be an unconditional `http://`. The compensating control
         * — adopting an https origin when the server redirects — only fires if
         * the server CHOOSES to redirect, so an on-path attacker answering the
         * cleartext probe with a plain 200 pinned the origin to http for the
         * life of the install, and `POST /auth/login` then carried the
         * password in the clear. The same thing happened with no attacker at
         * all whenever an operator's reverse proxy served content on :80
         * instead of redirecting. Choosing the safer default up front makes
         * redirect-adoption an upgrade path rather than the only defence.
         *
         * Internal so the unit suite can exercise the heuristic directly.
         */
        internal fun defaultSchemeFor(hostish: String): String {
            // Strip path / port to isolate the host portion: `host:port/path`
            // → `host`. IPv6 literals would arrive bracketed (`[::1]:7070`);
            // that path is unusual enough to leave to the user.
            val host = hostish
                .substringBefore('/')
                .substringBefore(':')
                .lowercase()
            val isPrivate = host == "localhost" ||
                host.endsWith(".local") ||
                host.matches(Regex("""^127\.\d+\.\d+\.\d+$""")) ||
                host.matches(Regex("""^10\.\d+\.\d+\.\d+$""")) ||
                host.matches(Regex("""^192\.168\.\d+\.\d+$""")) ||
                host.matches(Regex("""^172\.(1[6-9]|2\d|3[01])\.\d+\.\d+$""")) ||
                host == "::1"
            return if (isPrivate) "http" else "https"
        }
    }

    val serverUrl: Flow<String?> = context.dataStore.data.map { it[KEY_SERVER_URL] }
    val accessToken: Flow<String?> = context.dataStore.data.map { it[KEY_ACCESS_TOKEN]?.let(crypto::decrypt) }
    val refreshToken: Flow<String?> = context.dataStore.data.map { it[KEY_REFRESH_TOKEN]?.let(crypto::decrypt) }
    val assetToken: Flow<String?> = context.dataStore.data.map { it[KEY_ASSET_TOKEN]?.let(crypto::decrypt) }
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
    suspend fun getAssetToken(): String? = assetToken.first()

    suspend fun setServerUrl(url: String) {
        // Lowercase the scheme + strip trailing slashes. OkHttp's HttpUrl
        // parses scheme case-insensitively so this is cosmetic for the
        // API path — but Coil's fetcher matcher is case-SENSITIVE and
        // refuses `Https://` URLs with "Unable to create a fetcher".
        // Canonicalising on the way in keeps every consumer (Retrofit,
        // Coil, BaseUrlInterceptor) reading the same value.
        var trimmed = url.trim().trimEnd('/')
        // On a 10-foot keyboard users naturally type a bare host/IP
        // (192.168.1.50:7070, media.lan). Without a scheme, toHttpUrlOrNull()
        // returns null, the request silently stays on the localhost default, and
        // the user sees only "Could not connect" — dead-ending first-run on the
        // exact LAN/HTTP shape this app targets. An explicit scheme is kept.
        if (trimmed.isNotEmpty() && !trimmed.matches(Regex("^[a-zA-Z][a-zA-Z0-9+.-]*://.*"))) {
            trimmed = "${defaultSchemeFor(trimmed)}://$trimmed"
        }
        val canonical = tv.onscreen.android.data.normaliseScheme(trimmed)
        context.dataStore.edit { it[KEY_SERVER_URL] = canonical }
    }

    /** assetToken is nullable — a server that predates the asset-token
     *  work omits it; we clear any stale value in that case. */
    suspend fun setTokens(accessToken: String, refreshToken: String, assetToken: String? = null) {
        context.dataStore.edit {
            it[KEY_ACCESS_TOKEN] = crypto.encrypt(accessToken)
            it[KEY_REFRESH_TOKEN] = crypto.encrypt(refreshToken)
            if (assetToken.isNullOrEmpty()) {
                it.remove(KEY_ASSET_TOKEN)
            } else {
                it[KEY_ASSET_TOKEN] = crypto.encrypt(assetToken)
            }
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
            it.remove(KEY_ASSET_TOKEN)
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
