package tv.onscreen.mobile.data.repository

import retrofit2.HttpException
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.Marker
import tv.onscreen.mobile.data.model.ProgressRequest
import tv.onscreen.mobile.data.model.SearchResult
import tv.onscreen.mobile.data.model.WatchStatus
import tv.onscreen.mobile.data.model.WatchStatusRequest
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
open class ItemRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    open suspend fun getItem(id: String): ItemDetail = api.getItem(id).data

    open suspend fun getChildren(id: String): List<ChildItem> =
        api.getChildren(id).data

    open suspend fun updateProgress(
        itemId: String,
        offsetMs: Long,
        durationMs: Long,
        state: String,
    ) {
        api.updateProgress(itemId, ProgressRequest(offsetMs, durationMs, state))
    }

    /** Intro / credits markers for an episode. Movies and containers
     *  return an empty list; the network failure path also returns
     *  empty so the player doesn't fail-loud over an optional feature. */
    open suspend fun getMarkers(itemId: String): List<Marker> =
        try { api.getMarkers(itemId).data } catch (_: Exception) { emptyList() }

    open suspend fun search(query: String, limit: Int = 30, libraryId: String? = null): List<SearchResult> =
        api.search(query, limit, libraryId).data

    /**
     * Fetch lyrics for a track. Server returns 404 for non-track items
     * (movies, episodes) — we map that to null so the player overlay
     * silently hides for those types instead of error-banner-ing.
     *
     * The response carries both `plain` and `synced`. The player
     * prefers synced (LRC, line-by-line highlight); plain is the
     * fallback when LRCLIB has only an unstamped lyric. Both empty =
     * server cached "no lyrics" — also returned as null here.
     */
    open suspend fun getLyrics(itemId: String): tv.onscreen.mobile.data.model.LyricsResponse? {
        return try {
            val resp = api.getLyrics(itemId).data
            if (resp.plain.isEmpty() && resp.synced.isEmpty()) null else resp
        } catch (e: retrofit2.HttpException) {
            if (e.code() == 404) null else throw e
        }
    }

    /**
     * Read the per-user watching-status row for an item. Server returns
     * 404 when the user has never set a status; we map that to null so
     * callers can treat "no status" as a first-class state without
     * pattern-matching on HttpException.
     *
     * Any other error bubbles — the screen surfaces it like every other
     * fetch failure.
     */
    open suspend fun getWatchStatus(itemId: String): WatchStatus? {
        return try {
            WatchStatus.fromWire(api.getWatchStatus(itemId).data.status)
        } catch (e: HttpException) {
            if (e.code() == 404) null else throw e
        }
    }

    /** PUT a new status. Returns the post-write value so the UI can
     *  reflect it without a follow-up GET. */
    open suspend fun setWatchStatus(itemId: String, status: WatchStatus): WatchStatus? {
        val body = WatchStatusRequest(status.wire)
        return WatchStatus.fromWire(api.setWatchStatus(itemId, body).data.status)
    }

    /** Clear the row. Server is idempotent — 204 either way. */
    open suspend fun clearWatchStatus(itemId: String) {
        api.clearWatchStatus(itemId)
    }

    /** EXIF for a photo. Server returns 200 with empty fields when
     *  the photo has no EXIF block (PNG, scanner-skipped HEIC) — we
     *  surface that as the [tv.onscreen.mobile.data.model.PhotoExif]
     *  default-empty instance. Item-not-found maps to null. */
    open suspend fun getPhotoExif(itemId: String): tv.onscreen.mobile.data.model.PhotoExif? {
        return try {
            api.getPhotoExif(itemId).data
        } catch (e: retrofit2.HttpException) {
            if (e.code() == 404) null else throw e
        }
    }
}
