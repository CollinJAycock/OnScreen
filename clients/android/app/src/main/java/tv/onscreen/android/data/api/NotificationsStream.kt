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
import java.io.IOException
import java.util.concurrent.TimeUnit
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

    /**
     * SSE-tuned view of the shared client. An event stream is idle by
     * design — the server sends one `: keepalive` at connect and then
     * nothing until an event fires — so the shared client's 60 s read
     * timeout tore the connection down every minute, and the 5 s
     * reconnect delay left a recurring window where pushed events
     * (cross-device progress, "play on this TV") were dropped on the
     * floor. Zero disables the read timeout for this stream only;
     * connection loss still surfaces via onFailure. Shares the parent's
     * connection pool, interceptors, and TLS config.
     */
    private val sseClient: OkHttpClient =
        client.newBuilder()
            .readTimeout(0, TimeUnit.MILLISECONDS)
            .build()

    fun subscribe(): Flow<NotificationItem> = callbackFlow {
        val request = Request.Builder()
            .url("http://localhost/api/v1/notifications/stream")
            .header("Accept", "text/event-stream")
            .build()

        val source: EventSource = EventSources.createFactory(sseClient)
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

                // Both terminal callbacks MUST complete the flow
                // EXCEPTIONALLY. NotificationsRepository reconnects via
                // `.retry { }`, which only fires on an exception — a normal
                // completion instead propagates through `shareIn` into a
                // SharedFlow that never completes, so neither the retry nor
                // the callers' own outer reconnect loops ever run again and
                // every SSE-driven feature stays dead for the rest of the
                // session.
                override fun onClosed(eventSource: EventSource) {
                    close(IOException("SSE stream closed by server"))
                }

                override fun onFailure(
                    eventSource: EventSource,
                    t: Throwable?,
                    response: Response?,
                ) {
                    close(t ?: IOException("SSE stream failed: HTTP ${response?.code ?: 0}"))
                }
            })

        awaitClose { source.cancel() }
    }
}
