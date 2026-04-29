package tv.onscreen.android.data.repository

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.mapNotNull
import tv.onscreen.android.data.api.NotificationsStream
import tv.onscreen.android.data.model.ProgressUpdateData
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
            if (ev.type == PROGRESS_UPDATED_TYPE) ev.data else null
        }

    companion object {
        private const val PROGRESS_UPDATED_TYPE = "progress.updated"
    }
}
