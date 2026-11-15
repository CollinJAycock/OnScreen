package tv.onscreen.android.data.prefs

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.withContext

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
        // Switch the display to the video's frame rate (FrameRateSwitcher).
        // On by default; a TV or receiver that takes long to resync after a
        // mode change is the reason to turn it off.
        private val KEY_MATCH_FRAME_RATE = booleanPreferencesKey("match_frame_rate")
        // Files whose direct play this device couldn't read (Media3 ran out
        // of memory on the container's index — see
        // PlaybackHelper.isContainerFailure): comma-joined ids, oldest
        // first, at most REMUX_ONLY_MAX. Played as a remux from then on.
        private val KEY_REMUX_ONLY_FILES = stringPreferencesKey("remux_only_files")
        private const val REMUX_ONLY_SEP = ","
        internal const val REMUX_ONLY_MAX = 200

        /**
         * Pick a scheme for a bare-host URL the user typed into the server
         * field. Same rule as the phone client's normalizeServerUrl
         * (clients/android_native/.../ui/pair/PairViewModel.kt) — keep in step.
         *
         * Local-network hosts ([isLocalNetworkHost]) default to `http://`,
         * because the typical HomeLab box isn't running TLS. Everything else
         * defaults to `https://`, because a public DNS name almost always has
         * a cert (Cloudflare Tunnel, Tailscale Funnel, Let's Encrypt).
         *
         * The local test used to be RFC1918 / loopback / `.local` /
         * `localhost` only, so `media.lan`, a single-label name (`nas`) and a
         * Tailscale 100.64/10 address got https with no fallback, and a bare
         * LAN name dead-ended in "Could not connect".
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
            // → `host`, `[::1]:7070` → `::1`.
            val authority = hostish.substringBefore('/')
            val host = when {
                authority.startsWith("[") -> authority.substringBefore(']')
                // Two or more colons and no brackets: a bare IPv6 literal.
                authority.count { it == ':' } > 1 -> authority
                else -> authority.substringBefore(':')
            }
            return if (isLocalNetworkHost(host)) "http" else "https"
        }

        /**
         * True when [hostname] can only be a local-network (or loopback)
         * address: RFC1918 / CGNAT (Tailscale) / link-local / loopback IPv4,
         * ULA / link-local / loopback IPv6, single-label names and the
         * conventional local suffixes. A port of the canonical rule,
         * isLocalNetworkHost in clients/webos/src/lib/api/client.ts — keep the
         * two identical.
         */
        internal fun isLocalNetworkHost(hostname: String): Boolean {
            val h = hostname.lowercase().removePrefix("[").removeSuffix("]").removeSuffix(".")
            if (h.isEmpty()) return false
            if (h == "localhost" || h.endsWith(".localhost")) return true
            if (h.endsWith(".local") || h.endsWith(".lan") || h.endsWith(".home.arpa") || h.endsWith(".internal")) {
                return true
            }
            val v4 = Regex("""(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})""").matchEntire(h)
            if (v4 != null) {
                val a = v4.groupValues[1].toInt()
                val b = v4.groupValues[2].toInt()
                return a == 10 ||
                    a == 127 ||
                    (a == 172 && b in 16..31) ||
                    (a == 192 && b == 168) ||
                    (a == 169 && b == 254) ||
                    (a == 100 && b in 64..127)
            }
            if (h.contains(':')) {
                return h == "::1" ||
                    Regex("""^f[cd][0-9a-f]{2}:""").containsMatchIn(h) ||
                    Regex("""^fe[89ab][0-9a-f]:""").containsMatchIn(h)
            }
            // Single-label names ("nas", "plexbox") only resolve via local DNS.
            return !h.contains('.')
        }
    }

    val serverUrl: Flow<String?> = context.dataStore.data.map { it[KEY_SERVER_URL] }
    // Decrypting is an AndroidKeyStore operation — binder IPC to keystore2 —
    // and a map {} runs in the COLLECTOR's context, so every token read from
    // the main thread (the player, the Live TV screen, the background
    // service, sign-in) did that IPC on it. flowOn moves it to IO.
    val accessToken: Flow<String?> =
        context.dataStore.data.map { it[KEY_ACCESS_TOKEN]?.let(crypto::decrypt) }.flowOn(Dispatchers.IO)
    val refreshToken: Flow<String?> =
        context.dataStore.data.map { it[KEY_REFRESH_TOKEN]?.let(crypto::decrypt) }.flowOn(Dispatchers.IO)
    val assetToken: Flow<String?> =
        context.dataStore.data.map { it[KEY_ASSET_TOKEN]?.let(crypto::decrypt) }.flowOn(Dispatchers.IO)
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
        // (192.168.1.50:7070, media.lan, nas). Without a scheme, toHttpUrlOrNull()
        // returns null, the request silently stays on the localhost default, and
        // the user sees only "Could not connect" — dead-ending first-run on the
        // exact LAN/HTTP shape this app targets. A local-network host gets
        // http, anything else https (defaultSchemeFor); an explicit scheme is
        // kept.
        if (trimmed.isNotEmpty() && !trimmed.matches(Regex("^[a-zA-Z][a-zA-Z0-9+.-]*://.*"))) {
            trimmed = "${defaultSchemeFor(trimmed)}://$trimmed"
        }
        val canonical = tv.onscreen.android.data.normaliseScheme(trimmed)
        context.dataStore.edit { it[KEY_SERVER_URL] = canonical }
    }

    /** assetToken is nullable — a server that predates the asset-token
     *  work omits it; we clear any stale value in that case. */
    suspend fun setTokens(accessToken: String, refreshToken: String, assetToken: String? = null) {
        // Encrypted before the edit, on IO: DataStore runs an edit's
        // transform in the CALLER's context, so encrypting inside it did the
        // Keystore IPC on the main thread for a main-thread caller.
        val (access, refresh, asset) = withContext(Dispatchers.IO) {
            Triple(
                crypto.encrypt(accessToken),
                crypto.encrypt(refreshToken),
                assetToken?.takeIf { it.isNotEmpty() }?.let(crypto::encrypt),
            )
        }
        context.dataStore.edit {
            it[KEY_ACCESS_TOKEN] = access
            it[KEY_REFRESH_TOKEN] = refresh
            if (asset == null) {
                it.remove(KEY_ASSET_TOKEN)
            } else {
                it[KEY_ASSET_TOKEN] = asset
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
        context.dataStore.edit { removeAuth(it) }
    }

    /**
     * [clearAuth], but only while the stored refresh token is still
     * [expected] (null: none stored) — checked and cleared in ONE edit, so a
     * sign-in that stores fresh tokens between the caller's read and this
     * clear survives it. TokenAuthenticator's clears: it decides to sign out
     * from a read taken before the refresh call or the 401 it is answering,
     * and a blind clear wiped a sign-in that landed meanwhile (a re-dialed
     * stream's bare 401 racing the sign-in's write bounced the user straight
     * back to Login). Returns whether it cleared.
     */
    suspend fun clearAuthIfRefreshTokenIs(expected: String?): Boolean = withContext(Dispatchers.IO) {
        // On IO: the compare decrypts (Keystore IPC) inside the edit, which
        // runs in the caller's context.
        var cleared = false
        context.dataStore.edit { p ->
            if (p[KEY_REFRESH_TOKEN]?.let(crypto::decrypt) == expected) {
                removeAuth(p)
                cleared = true
            }
        }
        cleared
    }

    private fun removeAuth(p: androidx.datastore.preferences.core.MutablePreferences) {
        p.remove(KEY_ACCESS_TOKEN)
        p.remove(KEY_REFRESH_TOKEN)
        p.remove(KEY_ASSET_TOKEN)
        p.remove(KEY_USER_ID)
        p.remove(KEY_USERNAME)
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

    /** Whether playback switches the display to the video's frame rate. */
    val matchFrameRate: Flow<Boolean> = context.dataStore.data.map { it[KEY_MATCH_FRAME_RATE] ?: true }

    suspend fun setMatchFrameRate(enabled: Boolean) {
        context.dataStore.edit { it[KEY_MATCH_FRAME_RATE] = enabled }
    }

    /** Files this device plays as a remux instead of directly (see
     *  [addRemuxOnlyFile]). */
    suspend fun remuxOnlyFiles(): Set<String> =
        context.dataStore.data.first()[KEY_REMUX_ONLY_FILES]
            ?.split(REMUX_ONLY_SEP)?.filter { it.isNotEmpty() }?.toSet() ?: emptySet()

    /** Remember that [fileId]'s direct play failed reading the container, so
     *  the next play skips straight to a remux. Keeps the newest
     *  [REMUX_ONLY_MAX]. */
    suspend fun addRemuxOnlyFile(fileId: String) {
        context.dataStore.edit {
            val ids = it[KEY_REMUX_ONLY_FILES]?.split(REMUX_ONLY_SEP)?.filter { id -> id.isNotEmpty() && id != fileId }.orEmpty()
            it[KEY_REMUX_ONLY_FILES] = (ids + fileId).takeLast(REMUX_ONLY_MAX).joinToString(REMUX_ONLY_SEP)
        }
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
