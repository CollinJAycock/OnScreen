package tv.onscreen.android.data.api

import com.squareup.moshi.JsonAdapter
import com.squareup.moshi.Moshi
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.sse.EventSource
import okhttp3.sse.EventSourceListener
import okhttp3.sse.EventSources
import tv.onscreen.android.data.model.NotificationItem
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Subscribes to /api/v1/notifications/stream as an SSE source. The server
 * multiplexes user-facing notifications (item_added, scan_complete, etc.)
 * and cross-device sync events (`progress.updated`) on the same stream, so
 * subscribers receive every parsed [NotificationItem] and branch on the
 * `type` field. Reconnects are the caller's responsibility — typically a
 * coroutine that collects the flow and restarts on completion.
 *
 * The injected OkHttpClient carries [BaseUrlInterceptor] (rewrites
 * localhost → configured server) and [AuthInterceptor] (Bearer header), so
 * the SSE request authenticates and routes the same way regular API calls
 * do — no separate token plumbing here.
 */
@Singleton
class NotificationsStream @Inject constructor(
    private val client: OkHttpClient,
    moshi: Moshi,
) {
    private val adapter: JsonAdapter<NotificationItem> =
        moshi.adapter(NotificationItem::class.java)

    fun subscribe(): Flow<NotificationItem> = callbackFlow {
        val request = Request.Builder()
            .url("http://localhost/api/v1/notifications/stream")
            .header("Accept", "text/event-stream")
            .build()

        val source: EventSource = EventSources.createFactory(client)
            .newEventSource(request, object : EventSourceListener() {
                override fun onEvent(
                    eventSource: EventSource,
                    id: String?,
                    type: String?,
                    data: String,
                ) {
                    try {
                        val item = adapter.fromJson(data)
                        if (item != null) trySend(item)
                    } catch (_: Exception) {
                        // Malformed event — skip.
                    }
                }

                override fun onClosed(eventSource: EventSource) {
                    close()
                }

                override fun onFailure(
                    eventSource: EventSource,
                    t: Throwable?,
                    response: Response?,
                ) {
                    close(t)
                }
            })

        awaitClose { source.cancel() }
    }
}
