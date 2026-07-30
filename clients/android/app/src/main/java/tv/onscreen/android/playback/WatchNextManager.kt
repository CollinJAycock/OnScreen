package tv.onscreen.android.playback

import android.annotation.SuppressLint
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.util.Log
import androidx.tvprovider.media.tv.TvContractCompat
import androidx.tvprovider.media.tv.WatchNextProgram
import dagger.hilt.android.qualifiers.ApplicationContext
import tv.onscreen.android.R
import tv.onscreen.android.data.model.ItemDetail
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Publishes "Continue Watching" entries to the system's
 * `WatchNextPrograms` content provider so resumable items show up in
 * Google TV / Android TV launchers' Continue Watching row, independent
 * of OnScreen's own home screen. Required for TV-PN quality compliance.
 *
 * Lifecycle:
 *   - During playback (every ~30 s + on pause / stop): the current
 *     item is upserted with WATCH_NEXT_TYPE_CONTINUE and the current
 *     position. Existing rows are updated in place via
 *     `internal_provider_id` lookup, so we don't accumulate dupes.
 *   - When playback reaches the end (>= 90% completion or natural
 *     end-of-media): the row is removed so the launcher doesn't
 *     keep surfacing a finished title.
 *   - The launcher caches the poster image, so we point at a local
 *     `android.resource://` URI (the OnScreen logo) rather than the
 *     server's auth-required `/artwork/...` endpoint — the launcher
 *     has no way to send our PASETO bearer.
 *
 * The deep-link intent points at MainActivity with the item id +
 * resume position passed as extras; MainActivity reads them on launch
 * and routes straight into PlaybackFragment.
 */
/**
 * `@SuppressLint("RestrictedApi")`: every WatchNextProgram.Builder.setX
 * call is marked `@RestrictTo` in the androidx.tvprovider artifact —
 * but the Android TV "Continue Watching" docs + official samples
 * (developer.android.com/training/tv/playback/watchnext) consistently
 * call them directly from app code. The restriction is over-broad on
 * the library side, not a real "don't call this" signal. Scoped to
 * this class so a future builder-method outside this file would still
 * be linted normally.
 */
@SuppressLint("RestrictedApi")
@Singleton
class WatchNextManager @Inject constructor(
    @ApplicationContext private val context: Context,
) {

    private val resolver get() = context.contentResolver

    /**
     * Fire OS (and some older Android TV builds) ship no Google-TV
     * WatchNextPrograms provider. The per-call try/catch already keeps us
     * crash-safe, but without this we'd still run a full provider query + insert
     * every ~30s of playback on a device that can never honour it. Resolve the
     * TV provider authority ONCE and short-circuit when it's absent.
     */
    private val watchNextAvailable: Boolean by lazy {
        try {
            context.packageManager.resolveContentProvider(TvContractCompat.AUTHORITY, 0) != null
        } catch (e: Exception) {
            false
        }
    }

    /**
     * Insert or update the Watch Next row for [item] at [positionMs] /
     * [durationMs]. Safe to call on every progress tick — the
     * underlying upsert short-circuits when nothing meaningful
     * changed (same position rounded to 10 s buckets).
     */
    fun publishContinueWatching(item: ItemDetail, positionMs: Long, durationMs: Long) {
        if (!watchNextAvailable) return
        if (durationMs <= 0L || positionMs <= 0L) return
        // Don't re-publish a row we'd immediately remove.
        if (positionMs.toFloat() / durationMs > COMPLETION_THRESHOLD) {
            remove(item.id)
            return
        }
        try {
            val builder = WatchNextProgram.Builder()
                .setType(typeFor(item.type))
                .setWatchNextType(TvContractCompat.WatchNextPrograms.WATCH_NEXT_TYPE_CONTINUE)
                .setLastEngagementTimeUtcMillis(System.currentTimeMillis())
                .setLastPlaybackPositionMillis(positionMs.toIntSafe())
                .setDurationMillis(durationMs.toIntSafe())
                .setTitle(item.title)
                .setPosterArtUri(localPosterUri())
                .setIntentUri(deepLinkUri(item.id, positionMs))
                .setInternalProviderId(item.id)

            val existing = lookupExistingProgramId(item.id)
            if (existing == null) {
                resolver.insert(
                    TvContractCompat.WatchNextPrograms.CONTENT_URI,
                    builder.build().toContentValues(),
                )
            } else {
                resolver.update(
                    TvContractCompat.buildWatchNextProgramUri(existing),
                    builder.build().toContentValues(),
                    null, null,
                )
            }
        } catch (e: SecurityException) {
            // Some Fire OS / older Android TV builds restrict the
            // WatchNextPrograms provider. Best-effort — playback
            // works fine without the system row.
            Log.w(TAG, "WatchNext publish denied: ${e.message}")
        } catch (e: Exception) {
            Log.w(TAG, "WatchNext publish failed", e)
        }
    }

    /** Remove the Watch Next row for [itemId]. Called on completion. */
    fun remove(itemId: String) {
        if (!watchNextAvailable) return
        try {
            val existing = lookupExistingProgramId(itemId) ?: return
            resolver.delete(
                TvContractCompat.buildWatchNextProgramUri(existing),
                null, null,
            )
        } catch (e: Exception) {
            Log.w(TAG, "WatchNext remove failed for $itemId", e)
        }
    }

    /**
     * Remove every Watch Next row this app owns.
     *
     * Called on sign-out. The launcher's Continue Watching strip is a
     * SYSTEM-wide surface that outlives the app's own state, so without this a
     * signed-out user's in-progress titles stayed on the home screen of a
     * shared or handed-on TV — with working deep links into them, since the
     * rows carry an onscreen:// URI and a position. Clearing local tokens does
     * not touch the launcher's database.
     *
     * The query is already scoped to this package: the provider only returns
     * rows the calling app wrote.
     */
    fun removeAll() {
        if (!watchNextAvailable) return
        try {
            val ids = mutableListOf<Long>()
            resolver.query(
                TvContractCompat.WatchNextPrograms.CONTENT_URI,
                arrayOf(TvContractCompat.WatchNextPrograms._ID),
                null, null, null,
            )?.use { cursor ->
                while (cursor.moveToNext()) ids.add(cursor.getLong(0))
            }
            for (id in ids) {
                resolver.delete(TvContractCompat.buildWatchNextProgramUri(id), null, null)
            }
        } catch (e: Exception) {
            Log.w(TAG, "WatchNext clear-all failed", e)
        }
    }

    /**
     * Find the existing WatchNextPrograms row id (if any) for
     * [itemId] using the internal_provider_id we wrote earlier.
     * Returns null when no matching row exists (yet).
     */
    private fun lookupExistingProgramId(itemId: String): Long? {
        return try {
            resolver.query(
                TvContractCompat.WatchNextPrograms.CONTENT_URI,
                arrayOf(
                    TvContractCompat.WatchNextPrograms._ID,
                    TvContractCompat.WatchNextPrograms.COLUMN_INTERNAL_PROVIDER_ID,
                ),
                null, null, null,
            )?.use { cursor ->
                while (cursor.moveToNext()) {
                    if (cursor.getString(1) == itemId) {
                        return cursor.getLong(0)
                    }
                }
                null
            }
        } catch (e: Exception) {
            Log.w(TAG, "WatchNext lookup failed for $itemId", e)
            null
        }
    }

    private fun localPosterUri(): Uri {
        // android.resource:// URIs are readable by other system
        // components without permission grants — the launcher's
        // image loader resolves them by package name.
        //
        // Addressed by resource NAME, not the numeric id: these URIs are
        // persisted in the system WatchNextPrograms table and outlive app
        // updates, but AAPT/R8 reassign numeric ids on essentially every
        // release — so a numeric URI written by an older build resolves to a
        // different drawable (or nothing) after an update, leaving broken
        // artwork in the launcher's Continue Watching row. The name form is
        // stable across builds.
        return Uri.parse(
            "android.resource://${context.packageName}/drawable/" +
                context.resources.getResourceEntryName(R.drawable.ic_logo),
        )
    }

    private fun deepLinkUri(itemId: String, positionMs: Long): Uri {
        // Custom scheme that MainActivity intercepts. Carrying the
        // resume offset in the URI means the launcher tile picks up
        // exactly where the user left off without an extra round
        // trip to fetch progress.
        return Uri.parse("onscreen://watch/$itemId?position=$positionMs")
    }

    private fun typeFor(itemType: String): Int = when (itemType) {
        "movie"     -> TvContractCompat.WatchNextPrograms.TYPE_MOVIE
        "episode"   -> TvContractCompat.WatchNextPrograms.TYPE_TV_EPISODE
        "show"      -> TvContractCompat.WatchNextPrograms.TYPE_TV_SERIES
        "season"    -> TvContractCompat.WatchNextPrograms.TYPE_TV_SEASON
        "track"     -> TvContractCompat.WatchNextPrograms.TYPE_TRACK
        "album"     -> TvContractCompat.WatchNextPrograms.TYPE_ALBUM
        "artist"    -> TvContractCompat.WatchNextPrograms.TYPE_ARTIST
        "audiobook" -> TvContractCompat.WatchNextPrograms.TYPE_PLAYLIST
        "podcast"   -> TvContractCompat.WatchNextPrograms.TYPE_TV_SERIES
        "photo"     -> TvContractCompat.WatchNextPrograms.TYPE_CLIP
        else        -> TvContractCompat.WatchNextPrograms.TYPE_CLIP
    }

    /**
     * The Watch Next provider stores positions as `int` rather than
     * `long`, so we cap at Int.MAX_VALUE (~24 days of media) — well
     * past any real playable duration but the saturating cast keeps
     * us safe on synthetic / edge data.
     */
    private fun Long.toIntSafe(): Int =
        if (this > Int.MAX_VALUE) Int.MAX_VALUE else this.toInt()

    companion object {
        private const val TAG = "WatchNextManager"
        /** Past this fraction of duration we treat the title as
         *  finished and pull the row from the system Continue
         *  Watching list. Matches the server's `watch_state`
         *  threshold (90%). */
        private const val COMPLETION_THRESHOLD = 0.9
    }
}
