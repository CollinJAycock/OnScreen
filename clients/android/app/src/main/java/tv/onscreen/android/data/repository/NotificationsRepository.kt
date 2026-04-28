package tv.onscreen.android.data.repository

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.mapNotNull
import tv.onscreen.android.data.api.NotificationsStream
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.NotificationItem
import tv.onscreen.android.data.model.ProgressUpdateData
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
open class NotificationsRepository @Inject constructor(
    private val api: OnScreenApi,
    private val stream: NotificationsStream,
) {
    open suspend fun list(limit: Int = 50, offset: Int = 0): List<NotificationItem> =
        api.getNotifications(limit, offset).data

    open suspend fun unreadCount(): Long =
        try { api.getUnreadCount().data.count } catch (_: Exception) { 0L }

    open suspend fun markRead(id: String) {
        api.markNotificationRead(id)
    }

    open suspend fun markAllRead() {
        api.markAllNotificationsRead()
    }

    /** User-facing notifications only — sync events are filtered out so
     *  the bell-icon UI doesn't show a `progress.updated` row every time
     *  the user (or another of their devices) reports new playback
     *  progress. */
    open fun subscribe(): Flow<NotificationItem> =
        stream.subscribe().filter { it.type != PROGRESS_UPDATED_TYPE }

    /** Cross-device progress sync. Emits whenever the same user posts
     *  new progress on any item from any device, so the active player
     *  can update its resume position without polling. Each subscription
     *  opens its own underlying SSE — shared across consumers is a
     *  follow-up if connection count becomes an issue (currently a
     *  single playback fragment + the notifications list = 2 streams
     *  at most). */
    open fun subscribeProgressUpdates(): Flow<ProgressUpdateData> =
        stream.subscribe().mapNotNull { ev ->
            if (ev.type == PROGRESS_UPDATED_TYPE) ev.data else null
        }

    companion object {
        private const val PROGRESS_UPDATED_TYPE = "progress.updated"
    }
}
