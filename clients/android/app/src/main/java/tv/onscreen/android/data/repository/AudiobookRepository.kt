package tv.onscreen.android.data.repository

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.PlaybackRateRequest
import tv.onscreen.android.playback.AudiobookSpeed
import java.util.concurrent.ConcurrentHashMap
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Audiobook listening speed — GET/PUT /api/v1/items/{id}/playback-rate
 * (internal/api/v1/audiobook.go). Shared by the player and the background
 * media-session service, which fetches the next book's speed when it chains
 * to one.
 *
 * A speed picked on this device is kept as pending until its PUT succeeds,
 * and [rate] prefers it over the server's answer meanwhile — so a book
 * re-opened (or chained to) a moment later can't come back at the old
 * speed. A PUT that fails (offline, or a server without the route) leaves
 * it pending for the rest of the process: speed keeps working locally.
 */
@Singleton
open class AudiobookRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    /** App-lifetime scope for the fire-and-forget PUT, which must outlive
     *  the player screen. A seam for tests. */
    internal var detachedScope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /** Rates set on this device whose PUT hasn't succeeded, by book id. */
    private val pending = ConcurrentHashMap<String, Float>()

    /**
     * The speed [bookId] plays at; [itemId] is what the server is asked
     * about (the book or a chapter of it). Null when nothing is known — the
     * lookup failed, or the server predates the route (404). Never throws.
     */
    open suspend fun rate(itemId: String, bookId: String): Float? {
        val server = try {
            AudiobookSpeed.clamp(api.getPlaybackRate(itemId).data.rate)
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            null
        }
        // Read pending AFTER the GET: a pick made while it was in flight is
        // newer than the answer.
        return pending[bookId] ?: server
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
}
