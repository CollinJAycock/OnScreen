package tv.onscreen.android.data.repository

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.mapNotNull
import kotlinx.coroutines.flow.retry
import kotlinx.coroutines.flow.shareIn
import tv.onscreen.android.data.api.NotificationsStream
import tv.onscreen.android.data.model.NotificationItem
import tv.onscreen.android.data.model.PlaybackTransferData
import tv.onscreen.android.data.model.ProgressUpdateData
import tv.onscreen.android.data.model.asPlaybackTransfer
import tv.onscreen.android.data.model.asProgressUpdate
import javax.inject.Inject
import javax.inject.Singleton

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
 */
@Singleton
open class NotificationsRepository @Inject constructor(
    private val stream: NotificationsStream,
) {
    // App-lifetime scope for the shared SSE. The repository is a
    // @Singleton, so this lives as long as the process.
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

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
     * The underlying `callbackFlow` completes when the SSE drops
     * (onClosed / onFailure); `retry` reconnects after a short delay so
     * the shared stream stays alive across timeouts and server restarts.
     * Callers keep their own outer reconnect loops, but those now just
     * re-attach to this shared flow rather than opening a new socket.
     */
    /** Bumped on every identity transition (sign-in, sign-out, pairing).
     *  flatMapLatest cancels the current SSE call — closing the socket that
     *  was authenticated as the PREVIOUS user — and re-dials with the
     *  current bearer. Without this the singleton stream stayed bound to
     *  user A across a sign-out/sign-in on a shared TV: B got no
     *  cross-device sync, and A's "play on this TV" events kept driving
     *  B's session until the stale-stream watchdog happened to fire. */
    private val generation = kotlinx.coroutines.flow.MutableStateFlow(0)

    @OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
    private val events: Flow<NotificationItem> =
        generation
            .flatMapLatest {
                stream.subscribe().retry { delay(5_000); true }
            }
            .shareIn(scope, SharingStarted.WhileSubscribed(5_000), replay = 0)

    /** Tear down and re-dial the shared SSE under the current identity. */
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

    companion object {
        private const val PROGRESS_UPDATED_TYPE = "progress.updated"
        private const val PLAYBACK_TRANSFER_TYPE = "playback.transfer"
    }
}
