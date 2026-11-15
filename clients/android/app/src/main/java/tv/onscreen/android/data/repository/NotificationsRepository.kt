package tv.onscreen.android.data.repository

import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
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
import tv.onscreen.android.data.api.NotificationsStream
import tv.onscreen.android.data.api.PlaybackStop
import tv.onscreen.android.data.api.SseHttpException
import tv.onscreen.android.data.model.NotificationItem
import tv.onscreen.android.data.model.PlaybackStopData
import tv.onscreen.android.data.model.PlaybackTransferData
import tv.onscreen.android.data.model.ProgressUpdateData
import tv.onscreen.android.data.model.asPlaybackStop
import tv.onscreen.android.data.model.asPlaybackTransfer
import tv.onscreen.android.data.model.asProgressUpdate
import tv.onscreen.android.data.prefs.ServerPrefs
import java.util.concurrent.atomic.AtomicInteger
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.random.Random
import tv.onscreen.android.data.backgroundScope

/**
 * Channel-only wrapper around the server's notifications SSE stream.
 *
 * The visible "Notifications" screen has been removed; what remains is
 * cross-device progress sync, which still rides on the same SSE
 * channel. PlaybackFragment subscribes via [subscribeProgressUpdates]
 * so the active player can pick up a new resume position the moment
 * another of the user's devices reports one.
 *
 * v2.2 added [subscribePlaybackTransfers] for the cross-device "play on
 * Living Room TV" flow. The TV launches the player when an event
 * targets its `client_name`.
 *
 * [subscribePlaybackStops] carries the admin "stop this stream" event the
 * player obeys. Media-request notifications never reach this class — the
 * stream drops them (see isHiddenNotificationType).
 */
@Singleton
open class NotificationsRepository @Inject constructor(
    private val stream: NotificationsStream,
    private val prefs: ServerPrefs,
) {
    // App-lifetime scope for the shared SSE. The repository is a
    // @Singleton, so this lives as long as the process.
    private val scope = backgroundScope("NotificationsRepository")

    /** Bumped on every identity transition (sign-in, sign-out, pairing),
     *  AFTER the new tokens are stored (AuthRepository). flatMapLatest
     *  cancels the current SSE call — closing the socket that was
     *  authenticated as the PREVIOUS user — and re-dials with the current
     *  bearer. Without this the singleton stream stayed bound to user A
     *  across a sign-out/sign-in on a shared TV: B got no cross-device sync,
     *  and A's "play on this TV" events kept driving B's session. */
    private val generation = MutableStateFlow(0)

    /**
     * Single shared SSE connection, fanned out to every subscriber.
     *
     * Previously each of [subscribeProgressUpdates] /
     * [subscribePlaybackTransfers] called `stream.subscribe()`, opening
     * an independent SSE — so during playback MainActivity (transfers)
     * and PlaybackFragment (progress sync) held two concurrent
     * connections to the same endpoint. shareIn collapses them onto one
     * hot upstream: the SSE opens when the first subscriber arrives
     * (WhileSubscribed) and closes ~5 s after the last one leaves.
     *
     * Dialed only while signed in: signed out (the Login screen, where
     * MainActivity still subscribes) it used to 401 every 5 s. Every terminal
     * path of [NotificationsStream.subscribe] completes exceptionally, and
     * [reconnecting] re-dials with backoff ([SseReconnectPolicy]) — not the
     * flat 5 s forever it used to, which also answered a 429 TOO_MANY_SSE by
     * asking again every 5 s. Callers keep their own outer reconnect loops,
     * but those just re-attach to this shared flow rather than opening a new
     * socket.
     */
    @OptIn(ExperimentalCoroutinesApi::class)
    private val events: Flow<NotificationItem> by lazy {
        combine(prefs.isLoggedIn, generation) { loggedIn, gen -> if (loggedIn) gen else null }
            .distinctUntilChanged()
            .flatMapLatest { gen ->
                if (gen == null) {
                    emptyFlow()
                } else {
                    reconnecting { onOpen -> stream.subscribe(onOpen) }
                }
            }
            .shareIn(scope, SharingStarted.WhileSubscribed(5_000), replay = 0)
    }

    /** Tear down and re-dial the shared SSE under the current identity.
     *  Call after the identity's tokens are stored: a dial before that went
     *  out bare, and its 401 could clear the sign-in being written. */
    fun restartForIdentityChange() {
        generation.value++
    }

    /** Cross-device progress sync. Emits whenever the same user posts
     *  new progress on any item from any device, so the active player
     *  can update its resume position without polling. Filters the
     *  shared notifications stream. */
    open fun subscribeProgressUpdates(): Flow<ProgressUpdateData> =
        events.mapNotNull { ev ->
            if (ev.type == PROGRESS_UPDATED_TYPE) ev.asProgressUpdate() else null
        }

    /** Cross-device "play on…" handoff. Emits when another of the user's
     *  devices fires POST /playback/transfer targeting this TV. The
     *  caller MUST compare `target_client_name` against this device's
     *  registered client_name and only act when they match — the SSE
     *  channel is per-user, not per-device, so all of the user's
     *  subscribed clients receive every transfer event. */
    open fun subscribePlaybackTransfers(): Flow<PlaybackTransferData> =
        events.mapNotNull { ev ->
            if (ev.type == PLAYBACK_TRANSFER_TYPE) ev.asPlaybackTransfer() else null
        }

    /** Admin "stop this stream" (`playback.stop`). Like transfers, the event
     *  reaches every one of the user's players; the caller MUST check
     *  [PlaybackStop.targets] against what it is playing before acting. */
    open fun subscribePlaybackStops(): Flow<PlaybackStopData> =
        events.mapNotNull { ev ->
            if (ev.type == PlaybackStop.EVENT_TYPE) ev.asPlaybackStop() else null
        }

    companion object {
        private const val PROGRESS_UPDATED_TYPE = "progress.updated"
        private const val PLAYBACK_TRANSFER_TYPE = "playback.transfer"
    }
}

/**
 * [open] re-dialed per [SseReconnectPolicy] for as long as it allows. The
 * backoff restarts whenever a dial actually opens ([open]'s callback), so a
 * stream that ran for hours and then dropped comes back promptly, while one
 * that keeps failing (server down, LAN server out of reach, 429) backs off
 * to minutes. When the policy says stop (401) the flow just completes: the
 * sign-out teardown / identity key takes it from there, and a fresh
 * subscription re-dials. Ported from the phone client's
 * NotificationsRepository — keep in step.
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
 * The old policy — a flat 5 s, forever, whatever the error — kept dialing
 * while signed out (a 401 every 5 s from the Login screen) and answered a
 * 429 TOO_MANY_SSE (the server's per-user stream cap) by asking again every
 * 5 s. Same policy as the phone client.
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
     * TVs that lost a restarting server together don't re-dial in step.
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
