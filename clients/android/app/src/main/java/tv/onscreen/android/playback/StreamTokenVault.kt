package tv.onscreen.android.playback

import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.ResolvingDataSource

/**
 * Keeps playback credentials OUT of the URLs handed to ExoPlayer, and
 * re-attaches them at the moment the HTTP request is made. Ported from the
 * phone client (android_native `playback/StreamTokenVault.kt`).
 *
 * Why this exists: direct-play and HLS playlist URLs used to be built as
 * `…?token=<paseto>` and set straight onto the [androidx.media3.common.MediaItem].
 * When music is backgrounded, PlaybackFragment parks that player in
 * [OnScreenMediaSessionService], which wraps it in a Media3 MediaSession.
 * media3's legacy bridge copies `MediaItem.localConfiguration.uri` verbatim
 * into `METADATA_KEY_MEDIA_URI` on the active platform session, where any app
 * the user enabled as a notification listener (or any MEDIA_CONTENT_CONTROL
 * holder) can read it via `MediaSessionManager.getActiveSessions()` — and the
 * fallback credential is the 24 h user-wide purpose=asset token.
 *
 * The MediaItem therefore only ever carries the clean URL; [resolverFactory]
 * appends `?token=` to the outgoing DataSpec below the player, where nothing
 * re-broadcasts it.
 *
 * HLS: only the top-level playlist URL is registered. The server rewrites every
 * child URI (variant playlists, segments, init maps) with the token embedded in
 * the playlist BODY (`rewritePlaylist` / `StaticMaster` / `StaticRung`), so child
 * requests authenticate on their own and never reach the session metadata. The
 * playlist re-polls hit the exact registered URL, so an exact-match key is
 * sufficient and no path-prefix keying is needed.
 *
 * Service and UI share this object in-process (the service declares no
 * `android:process`). Nothing is persisted; tokens are short-lived and a cold
 * start re-resolves them from the item fetch.
 */
object StreamTokenVault {

    /**
     * Bounded LRU. ACCESS-ordered, so both [register] and every request the
     * resolver makes move a url to the tail, and the head is always the
     * least-recently-used credential. [LinkedHashMap.removeEldestEntry] runs
     * after the insert, so the entry just registered (the tail) is never the
     * one evicted.
     *
     * This used to be a ConcurrentHashMap trimmed with `keys.firstOrNull()` —
     * HASH order, not insertion order — so once over the cap, register() could
     * evict the token it had just stored, or the one the player was using; the
     * next request (a seek, a playlist re-poll) then went out bare, 401'd and
     * playback died. Access order mutates on get(), so every touch of [tokens]
     * goes through [lock].
     */
    private val lock = Any()
    private val tokens = object : LinkedHashMap<String, String>(16, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, String>?): Boolean =
            size > MAX_ENTRIES
    }

    /**
     * Record [token] as the credential for [cleanUrl] and return [cleanUrl]
     * unchanged, so callers can write `setUri(vault.register(url, token))`.
     * A null/blank token registers nothing.
     */
    fun register(cleanUrl: String, token: String?): String {
        // Bounded by MAX_ENTRIES (see [tokens]). Playback touches a handful of
        // URLs per session; eviction only trips if something loops, and then
        // drops the least-recently-used credential, never the new or live one.
        if (!token.isNullOrEmpty()) synchronized(lock) { tokens[cleanUrl] = token }
        return cleanUrl
    }

    /** Drop every credential. Called on identity transitions (logout,
     *  involuntary sign-out) so a signed-out user's tokens do not linger in
     *  memory for the next account. */
    fun clear() = synchronized(lock) { tokens.clear() }

    /** The credential for [url], marking it most-recently-used — what the
     *  resolver calls per request, so an in-use url stays clear of eviction. */
    internal fun resolve(url: String): String? = synchronized(lock) { tokens[url] }

    /** Read back a registered credential so tests can assert that a url is
     *  clean AND that its token was actually captured. A peek: iterating does
     *  not reorder an access-ordered map, so asserting never changes which
     *  entry is evicted next. */
    @androidx.annotation.VisibleForTesting
    fun tokenForTest(cleanUrl: String): String? =
        synchronized(lock) { tokens.entries.firstOrNull { it.key == cleanUrl }?.value }

    /**
     * Wraps [upstream] so each request gains its `?token=` immediately before
     * the socket opens. Unknown URLs (HLS children that already carry their own
     * credential, subtitle side-loads) pass through untouched.
     */
    @UnstableApi
    fun resolverFactory(upstream: DataSource.Factory): DataSource.Factory =
        ResolvingDataSource.Factory(upstream) { dataSpec ->
            val token = resolve(dataSpec.uri.toString())
            if (token.isNullOrEmpty()) {
                dataSpec
            } else {
                dataSpec.withUri(
                    dataSpec.uri.buildUpon().appendQueryParameter("token", token).build(),
                )
            }
        }

    /**
     * Strip a `token` query parameter from a URL that already carries one
     * (e.g. the server's HLS `playlist_url`). Returns the clean URL and the
     * decoded token (null when there was none). Pure string handling — no
     * android.net.Uri — so it behaves identically in JVM unit tests.
     */
    fun split(url: String): Pair<String, String?> {
        val q = url.indexOf('?')
        if (q < 0) return url to null
        val hash = url.indexOf('#', q)
        val query = if (hash >= 0) url.substring(q + 1, hash) else url.substring(q + 1)
        val fragment = if (hash >= 0) url.substring(hash) else ""
        var token: String? = null
        val kept = query.split('&').filter { part ->
            val name = part.substringBefore('=')
            if (name == "token") {
                if (token == null) {
                    token = runCatching {
                        java.net.URLDecoder.decode(part.substringAfter('=', ""), "UTF-8")
                    }.getOrNull()
                }
                false
            } else {
                part.isNotEmpty()
            }
        }
        if (token.isNullOrEmpty()) return url to null
        val base = url.substring(0, q)
        val cleaned = if (kept.isEmpty()) base + fragment else base + "?" + kept.joinToString("&") + fragment
        return cleaned to token
    }

    /** Internal so the eviction tests fill to the real cap. */
    internal const val MAX_ENTRIES = 64
}
