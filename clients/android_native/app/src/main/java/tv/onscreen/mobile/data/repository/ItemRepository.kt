package tv.onscreen.mobile.data.repository

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
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
    /** App-lifetime scope for fire-and-forget writes that must outlive
     *  the caller. The terminal 'stopped' progress event is the
     *  scrobble trigger server-side, and an auto-advancing music track
     *  pops the player off the back stack — cancelling its
     *  viewModelScope — the instant the finished track would report
     *  'stopped'. Riding that scope drops the PUT mid-flight, so the
     *  listen never scrobbles; this scope is never cancelled. */
    private val detachedScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    open suspend fun getItem(id: String): ItemDetail = api.getItem(id).data

    open suspend fun getChildren(id: String): List<ChildItem> =
        api.getChildren(id).data

    open suspend fun updateProgress(
        itemId: String,
        offsetMs: Long,
        durationMs: Long,
        state: String,
        decision: String? = null,
    ) {
        api.updateProgress(itemId, ProgressRequest(offsetMs, durationMs, state, decision))
    }

    /** Fire-and-forget progress publish on the app-lifetime
     *  [detachedScope]. Use for the terminal 'stopped' event, which
     *  must reach the server even as the player screen + ViewModel are
     *  being torn down by an auto-advance (a viewModelScope.launch
     *  would be cancelled before the PUT lands). Best-effort — server
     *  unreachability is swallowed. */
    open fun reportProgressDetached(
        itemId: String,
        offsetMs: Long,
        durationMs: Long,
        state: String,
        decision: String? = null,
    ) {
        detachedScope.launch {
            try {
                updateProgress(itemId, offsetMs, durationMs, state, decision)
            } catch (_: Exception) { }
        }
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
     * Read the per-user watching-status row for an item. Server always
     * returns 200; when the user has no recorded status, `data.status`
     * is the empty string. `WatchStatus.fromWire("")` yields null, so
     * callers get a single null sentinel for "no status set" without
     * having to pattern-match on HTTP codes.
     *
     * (Pre-server-PR-#27 this endpoint returned 404 for the no-row
     * case, which made the browser surface a "Failed to load
     * resource" console error on every visit — fixed server-side by
     * collapsing both paths to 200. The Android client never saw the
     * console error, but the contract change still simplifies us.)
     *
     * Any non-200 (network blip, server 5xx, auth expiry) bubbles —
     * the screen surfaces it like every other fetch failure.
     */
    open suspend fun getWatchStatus(itemId: String): WatchStatus? {
        // .status, not .data.status: the watch-status server handler writes
        // a BARE `{status,created_at,updated_at}` body (respond.JSON), not
        // the `{data:{…}}` envelope the rest of the API uses. The Retrofit
        // type is now WatchStatusResponse (not ApiResponse<…>) to match —
        // parsing the envelope made Moshi throw "Required value 'data'
        // missing" on every read, so a set status never loaded.
        return WatchStatus.fromWire(api.getWatchStatus(itemId).status)
    }

    /** PUT a new status. Returns the post-write value so the UI can
     *  reflect it without a follow-up GET. */
    open suspend fun setWatchStatus(itemId: String, status: WatchStatus): WatchStatus? {
        val body = WatchStatusRequest(status.wire)
        // .status, not .data.status (see getWatchStatus): the PUT succeeds
        // server-side but returns the same bare body. Parsing it as an
        // envelope threw AFTER the row had already changed, so the caller's
        // optimistic UI flip was reverted even though the write landed.
        return WatchStatus.fromWire(api.setWatchStatus(itemId, body).status)
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
