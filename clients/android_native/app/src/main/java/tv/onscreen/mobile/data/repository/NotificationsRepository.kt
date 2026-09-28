package tv.onscreen.mobile.data.repository

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.catch
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.emitAll
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.mapNotNull
import kotlinx.coroutines.flow.retryWhen
import kotlinx.coroutines.flow.shareIn
import tv.onscreen.mobile.data.api.NotificationsStream
import tv.onscreen.mobile.data.api.SseHttpException
import tv.onscreen.mobile.data.model.NotificationItem
import tv.onscreen.mobile.data.model.PlaybackStop
import tv.onscreen.mobile.data.model.PlaybackStopEvent
import tv.onscreen.mobile.data.model.ProgressUpdateData
import tv.onscreen.mobile.data.model.asPlaybackStop
import tv.onscreen.mobile.data.model.asProgressUpdate
import tv.onscreen.mobile.data.prefs.ServerPrefs
import java.util.concurrent.atomic.AtomicInteger
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.random.Random

/**
 * Channel-only wrapper around the server's notifications SSE stream.
 *
 * The phone app has no visible notifications screen; the stream carries the
 * sync events players act on:
 *  - [subscribeProgressUpdates] — cross-device resume (`progress.updated`);
 *  - [subscribePlaybackStops] — the admin "stop this stream" (`playback.stop`).
 * Media-request notifications are dropped by [NotificationsStream] and never
 * reach a subscriber.
 */
@Singleton
open class NotificationsRepository @Inject constructor(
    private val stream: NotificationsStream,
    private val prefs: ServerPrefs,
) {
    // App-lifetime scope for the shared SSE (the repository is a @Singleton).
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /**
     * One SSE connection fanned out to every subscriber (the video player's
     * progress + stop collectors, and any audio-side collector), instead of
     * one socket per subscription — the server caps SSE connections per user.
     * Opens when the first subscriber arrives and closes ~5 s after the last
     * one leaves.
     *
     * Keyed on the signed-in identity: a sign-out closes the socket and a
     * different account re-dials with its own bearer, so the previous user's
     * events can never drive the next user's player. Every terminal path of
     * [NotificationsStream.subscribe] completes exceptionally, and
     * [reconnecting] re-dials with backoff ([SseReconnectPolicy]), so the
     * shared stream survives network drops and server restarts without
     * hammering an unreachable server or the per-user SSE cap.
     */
    @OptIn(ExperimentalCoroutinesApi::class)
    private val events: Flow<NotificationItem> by lazy {
        combine(prefs.serverUrl, prefs.userId, prefs.isLoggedIn) { url, user, loggedIn ->
            if (url.isNullOrEmpty() || !loggedIn) null else "$url|${user.orEmpty()}"
        }
            .distinctUntilChanged()
            .flatMapLatest { identity ->
                if (identity == null) {
                    emptyFlow()
                } else {
                    reconnecting { onOpen -> stream.subscribe(onOpen) }
                }
            }
            .shareIn(scope, SharingStarted.WhileSubscribed(5_000), replay = 0)
    }

    /** Cross-device progress sync. Emits whenever the same user posts
     *  new progress on any item from any device, so the active player
     *  can update its resume position without polling. */
    open fun subscribeProgressUpdates(): Flow<ProgressUpdateData> =
        events.mapNotNull { ev ->
            if (ev.type == PROGRESS_UPDATED_TYPE) ev.asProgressUpdate() else null
        }

    /**
     * Admin "stop this stream" events (`playback.stop`). The channel is
     * per-user, so this emits every stop for any of the user's players:
     * the collector MUST check [PlaybackStopEvent.targets] against what it is
     * playing (item id, its transcode session id if any, its client name if
     * it reports one) and only then stop, showing
     * [PlaybackStopEvent.displayText]. Used by PlayerViewModel (video and
     * screen-owned audio); background audio can collect it the same way.
     */
    open fun subscribePlaybackStops(): Flow<PlaybackStopEvent> =
        events.mapNotNull { ev ->
            if (ev.type == PlaybackStop.EVENT_TYPE) ev.asPlaybackStop() else null
        }

    private companion object {
        const val PROGRESS_UPDATED_TYPE = "progress.updated"
    }
}

/**
 * [open] re-dialed per [SseReconnectPolicy] for as long as it allows. The
 * backoff restarts whenever a dial actually opens ([open]'s callback), so a
 * stream that ran for hours and then dropped comes back promptly, while one
 * that keeps failing (server down, LAN server out of reach, 429) backs off
 * to minutes. When the policy says stop (401) the flow just completes: the
 * sign-out teardown / identity key takes it from there, and a fresh
 * subscription re-dials.
 */
internal fun <T> reconnecting(
    random: Random = Random.Default,
    open: (onOpen: () -> Unit) -> Flow<T>,
): Flow<T> = flow {
    // Consecutive failed dials since the stream last opened. onOpen runs on
    // an OkHttp thread.
    val failures = AtomicInteger(0)
    emitAll(
        open { failures.set(0) }
            .retryWhen { cause, _ ->
                val wait = SseReconnectPolicy.delayMs(cause, failures.getAndIncrement(), random)
                    ?: return@retryWhen false
                delay(wait)
                true
            }
            // Only a stop gets here — keep it from failing the shared scope.
            .catch { },
    )
}

/**
 * How long to wait before re-dialing the notifications SSE, by why it ended.
 *
 * The old policy — a flat 5 s, forever, whatever the error — kept a phone
 * whose server was unreachable (off the home LAN) waking the radio every 5 s,
 * and answered a 429 TOO_MANY_SSE (the server's per-user stream cap) by
 * asking again every 5 s.
 */
internal object SseReconnectPolicy {
    /** Ceiling of the first re-dial after a network drop / server close. */
    const val BASE_DELAY_MS = 5_000L

    /** Ceiling of the first re-dial after a 4xx (429 TOO_MANY_SSE or any
     *  other client error): asking again at once is refused the same way. */
    const val CLIENT_ERROR_DELAY_MS = 60_000L

    const val MAX_DELAY_MS = 5 * 60_000L

    /**
     * Delay before the next dial, [failures] being the consecutive failed
     * dials since the stream last opened (0 = first retry), or null to stop.
     * Exponential from the cause's floor, capped at [MAX_DELAY_MS], with
     * "equal jitter" (half fixed, half random in [ceiling/2, ceiling]) so the
     * phones that lost a restarting server together don't re-dial in step.
     */
    fun delayMs(cause: Throwable, failures: Int, random: Random = Random.Default): Long? {
        val code = (cause as? SseHttpException)?.code
        // The SSE call runs through the shared client, so a 401 only reaches
        // here after TokenAuthenticator failed to refresh past it. A definitive
        // rejection has already signed out (the identity key closes this
        // stream anyway); a transient one is retried by the next subscription
        // rather than every few seconds against a session we can't renew.
        if (code == 401) return null
        val floor = if (code != null && code in 400..499) CLIENT_ERROR_DELAY_MS else BASE_DELAY_MS
        val ceiling = (floor shl failures.coerceIn(0, 16)).coerceAtMost(MAX_DELAY_MS)
        val half = ceiling / 2
        return half + random.nextLong(half + 1)
    }
}
