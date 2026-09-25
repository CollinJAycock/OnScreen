package tv.onscreen.android.data.api

import kotlinx.coroutines.CancellationException
import retrofit2.HttpException

/**
 * Why the server refused a 'playing' progress heartbeat.
 *
 * The progress route answers 403 exactly when the server no longer lets this
 * user watch the item: the parental watch limit (PARENTAL_LIMIT — daily cap
 * reached, allowed-hours window closed) or checkLibraryAccess (library grant
 * revoked, content-rating ceiling lowered mid-session). EVERY such 403 must
 * stop playback — foreground (ProgressTracker) and background
 * (OnScreenMediaSessionService) alike — or a stream on an already-issued token
 * keeps going. Only 'playing' is gated server-side; a refused pause/stop
 * report is not a refusal of playback.
 *
 * Single source of the decision for every heartbeat on this client. Kept in
 * step with the phone client's `data/api/HeartbeatRefusal.kt`.
 */
sealed class HeartbeatRefusal {
    /** PARENTAL_LIMIT; [reason] is the server's reason code
     *  (`daily_limit_reached`, `outside_allowed_hours`, …), or null. */
    data class WatchLimit(val reason: String?) : HeartbeatRefusal()

    /** Any other 403 — the item left this profile's reach mid-session. */
    data object ContentRevoked : HeartbeatRefusal()

    companion object {
        /** The refusal [e] represents for a report in [state], or null when it
         *  is not one (another state, another status, a transport failure). */
        fun of(state: String, e: Throwable): HeartbeatRefusal? {
            if (state != "playing" || e !is HttpException || e.code() != 403) return null
            val err = e.apiError()
            return if (err?.code == "PARENTAL_LIMIT") WatchLimit(err.message) else ContentRevoked
        }

        /** Send one 'playing' heartbeat via [send]. Returns the refusal when the
         *  server refused it; null on success and on any other failure, which
         *  stays best-effort (the next tick retries). Cancellation propagates. */
        suspend fun heartbeat(send: suspend () -> Unit): HeartbeatRefusal? = try {
            send()
            null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            of("playing", e)
        }
    }
}
