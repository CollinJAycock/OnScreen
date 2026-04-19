package tv.onscreen.android.data.repository

import kotlinx.coroutines.flow.Flow
import tv.onscreen.android.data.api.NotificationsStream
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.NotificationItem
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

    open fun subscribe(): Flow<NotificationItem> = stream.subscribe()
}
