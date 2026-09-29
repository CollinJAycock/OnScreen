package tv.onscreen.mobile.data.repository

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import retrofit2.HttpException
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.Bookmark
import tv.onscreen.mobile.data.model.BookmarkNoteRequest
import tv.onscreen.mobile.data.model.CreateBookmarkRequest
import tv.onscreen.mobile.data.model.PlaybackRateRequest
import tv.onscreen.mobile.playback.AudiobookSpeed
import java.util.concurrent.ConcurrentHashMap
import javax.inject.Inject
import javax.inject.Singleton

/** A book's listening speed as far as this device knows it. */
data class ListeningSpeed(
    /** The speed to play at; null when nothing is known (play at 1×). */
    val rate: Float?,
    /** False when the server predates the audiobook routes (it answered
     *  404): speed still works locally, bookmarks are hidden. */
    val serverSupport: Boolean,
)

/**
 * Audiobook listening speed and bookmarks. Server contract:
 * internal/api/v1/audiobook.go.
 *
 * Shared by the player screen and the background [PlaybackService] (both
 * inject this singleton), which is what keeps a speed the listener just
 * picked from being overwritten: [saveRate] records it as pending before
 * the PUT goes out, and [listeningSpeed] prefers a pending rate over what
 * the server says until the PUT lands. So a chapter the service chains to
 * a second later — or the next screen that opens — plays at the new speed
 * even if the write is still in flight. A PUT that fails (offline, or a
 * server without the route) leaves the rate pending for the rest of the
 * process: speed keeps working locally, it just isn't saved.
 */
@Singleton
open class AudiobookRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    /** App-lifetime scope for the fire-and-forget PUT, which must outlive
     *  the screen that changed the speed. A seam for tests. */
    internal var detachedScope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /** Rates set on this device whose PUT hasn't succeeded, by book id. */
    private val pending = ConcurrentHashMap<String, Float>()

    /**
     * The speed [bookId] plays at. [itemId] is what the server is asked
     * about (the book, or the chapter being played — it resolves either to
     * the book). Never throws: a failed lookup is "nothing known", a 404 is
     * a server without the feature.
     */
    open suspend fun listeningSpeed(itemId: String, bookId: String): ListeningSpeed {
        val server = try {
            AudiobookSpeed.clamp(api.getPlaybackRate(itemId).data.rate)
        } catch (e: CancellationException) {
            throw e
        } catch (e: HttpException) {
            return ListeningSpeed(pending[bookId], serverSupport = e.code() != 404)
        } catch (_: Exception) {
            return ListeningSpeed(pending[bookId], serverSupport = true)
        }
        // Read pending AFTER the GET: a speed picked while it was in flight
        // is newer than the answer.
        return ListeningSpeed(pending[bookId] ?: server, serverSupport = true)
    }

    /** Save [rate] for [bookId] (PUT against [itemId]), fire-and-forget. */
    open fun saveRate(itemId: String, bookId: String, rate: Float) {
        val clamped = AudiobookSpeed.clamp(rate)
        pending[bookId] = clamped
        detachedScope.launch {
            try {
                api.setPlaybackRate(itemId, PlaybackRateRequest(clamped.toDouble()))
                // Only if no newer pick replaced it meanwhile.
                pending.remove(bookId, clamped)
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                // Stays pending: the speed still applies on this device.
            }
        }
    }

    /**
     * Every bookmark the caller has in the book ([itemId] = the book or one
     * of its chapters), in listening order. Null when the server predates
     * bookmarks (404) — the caller hides the list. Other failures throw.
     */
    open suspend fun bookmarks(itemId: String): List<Bookmark>? = try {
        api.listBookmarks(itemId).data
    } catch (e: HttpException) {
        if (e.code() == 404) null else throw e
    }

    /** Bookmark [positionMs] in the playable item [itemId] (a single-file
     *  book, or the chapter). [note] is sent trimmed. Throws on failure —
     *  409 BOOKMARK_LIMIT, 404 on an older server. */
    open suspend fun addBookmark(itemId: String, positionMs: Long, note: String): Bookmark =
        api.createBookmark(itemId, CreateBookmarkRequest(positionMs.coerceAtLeast(0L), note.trim())).data

    /** Replace a bookmark's note ("" clears it). Throws on failure. */
    open suspend fun updateNote(bookmarkId: String, note: String) {
        api.updateBookmark(bookmarkId, BookmarkNoteRequest(note.trim()))
    }

    /** Delete a bookmark. Throws on failure. */
    open suspend fun deleteBookmark(bookmarkId: String) {
        api.deleteBookmark(bookmarkId)
    }

    companion object {
        /** Matches maxBookmarkNoteRunes on the server (the note CHECK). */
        const val NOTE_MAX = 500
    }
}
