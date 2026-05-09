package tv.onscreen.android.data.repository

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.mapNotNull
import tv.onscreen.android.data.api.NotificationsStream
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
    /** Cross-device progress sync. Emits whenever the same user posts
     *  new progress on any item from any device, so the active player
     *  can update its resume position without polling. Each subscription
     *  opens its own underlying SSE. */
    open fun subscribeProgressUpdates(): Flow<ProgressUpdateData> =
        stream.subscribe().mapNotNull { ev ->
            if (ev.type == PROGRESS_UPDATED_TYPE) ev.asProgressUpdate() else null
        }

    /** Cross-device "play on…" handoff. Emits when another of the user's
     *  devices fires POST /playback/transfer targeting this TV. The
     *  caller MUST compare `target_client_name` against this device's
     *  registered client_name and only act when they match — the SSE
     *  channel is per-user, not per-device, so all of the user's
     *  subscribed clients receive every transfer event. */
    open fun subscribePlaybackTransfers(): Flow<PlaybackTransferData> =
        stream.subscribe().mapNotNull { ev ->
            if (ev.type == PLAYBACK_TRANSFER_TYPE) ev.asPlaybackTransfer() else null
        }

    companion object {
        private const val PROGRESS_UPDATED_TYPE = "progress.updated"
        private const val PLAYBACK_TRANSFER_TYPE = "playback.transfer"
    }
}
