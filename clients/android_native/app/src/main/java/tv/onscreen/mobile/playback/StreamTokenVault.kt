package tv.onscreen.mobile.playback

import android.net.Uri
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.ResolvingDataSource

/**
 * Keeps playback credentials OUT of the URLs we hand to ExoPlayer, and
 * re-attaches them at the moment the HTTP request is made.
 *
 * Why this exists: a direct-play URL used to be built as
 * `…/media/stream/<fileId>?token=<paseto>` and set straight onto the
 * [androidx.media3.common.MediaItem]. For audio that MediaItem reaches the
 * service's [androidx.media3.session.MediaSession], and media3's legacy
 * bridge (`ControllerLegacyCbForBroadcast.updateMetadataIfChanged` →
 * `LegacyConversions.convertToMediaMetadataCompat`) copies
 * `MediaItem.localConfiguration.uri` verbatim into
 * `METADATA_KEY_MEDIA_URI` on an ACTIVE platform session. That callback is
 * always on — it does not wait for a controller to connect. Any app the user
 * has enabled as a notification listener (one Settings toggle, routinely
 * requested by watch companions, automation and notification-history apps)
 * can then call `MediaSessionManager.getActiveSessions()` and read the token
 * straight out of the metadata. No OnScreen permission, no root, no ADB.
 *
 * The fix is to keep the credential out of the MediaItem entirely: the player
 * only ever sees the clean URL, and [resolverFactory] appends `?token=` to the
 * outgoing [androidx.media3.datasource.DataSpec] below the player, where
 * nothing re-broadcasts it.
 *
 * The service and the UI share this object rather than passing tokens over the
 * MediaController IPC boundary — `PlaybackService` declares no
 * `android:process`, so both live in the app process and a plain in-memory map
 * is the whole channel. Nothing here is persisted: tokens are short-lived and
 * a cold start re-resolves them from the item fetch.
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
     * next request (a seek, a reconnect) then went out bare, 401'd and
     * playback died. Access order mutates on get(), so every touch of [tokens]
     * goes through [lock]. Same structure as the TV client's vault.
     */
    private val lock = Any()
    private val tokens = object : LinkedHashMap<String, String>(16, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, String>?): Boolean =
            size > MAX_ENTRIES
    }

    /**
     * Record [token] as the credential for [cleanUrl] and return [cleanUrl]
     * unchanged, so callers can write `setUri(vault.register(url, token))`.
     * A null/blank token registers nothing — some servers and all `file://`
     * offline sources need no credential.
     */
    fun register(cleanUrl: String, token: String?): String {
        // Bounded by MAX_ENTRIES (see [tokens]). Playback touches a handful of
        // URLs per session; eviction only trips if something loops, and then
        // drops the least-recently-used credential, never the new or live one.
        if (!token.isNullOrEmpty()) synchronized(lock) { tokens[cleanUrl] = token }
        return cleanUrl
    }

    /** Drop every credential. Called on identity transitions so a signed-out
     *  user's tokens do not linger in memory for the next account. */
    fun clear() = synchronized(lock) { tokens.clear() }

    /** The credential for [url], marking it most-recently-used — what the
     *  resolver calls per request, so an in-use url stays clear of eviction. */
    internal fun resolve(url: String): String? = synchronized(lock) { tokens[url] }

    /** Read back a registered credential. Exists so tests can assert that a
     *  url is clean AND that its token was actually captured — asserting only
     *  the first would pass if the token were silently dropped. A peek:
     *  iterating does not reorder an access-ordered map, so asserting never
     *  changes which entry is evicted next. */
    @androidx.annotation.VisibleForTesting
    fun tokenForTest(cleanUrl: String): String? =
        synchronized(lock) { tokens.entries.firstOrNull { it.key == cleanUrl }?.value }

    /**
     * Wraps [upstream] so each request gains its `?token=` immediately before
     * the socket opens. Unknown URLs (offline `file://`, HLS transcode URLs
     * that already carry their own credential) pass through untouched.
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
     * Strip a `token` query parameter from a URL that already carries one.
     * Belt-and-braces for any path that still builds a credentialed URL: the
     * clean form is what gets registered and handed to the player.
     */
    fun split(url: String): Pair<String, String?> {
        val uri = runCatching { Uri.parse(url) }.getOrNull() ?: return url to null
        val token = runCatching { uri.getQueryParameter("token") }.getOrNull()
        if (token.isNullOrEmpty()) return url to null
        val cleaned = uri.buildUpon().clearQuery().apply {
            uri.queryParameterNames
                .filter { it != "token" }
                .forEach { name ->
                    uri.getQueryParameters(name).forEach { appendQueryParameter(name, it) }
                }
        }.build().toString()
        return cleaned to token
    }

    /** Internal so the eviction tests fill to the real cap. */
    internal const val MAX_ENTRIES = 64
}
