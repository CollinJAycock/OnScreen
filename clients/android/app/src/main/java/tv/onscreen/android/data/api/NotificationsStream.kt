package tv.onscreen.android.data.api

import com.squareup.moshi.JsonAdapter
import com.squareup.moshi.Moshi
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
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
            // callTimeout MUST be cleared too. newBuilder() inherits every
            // setting from the parent, and the parent now carries a whole-call
            // ceiling — which for a deliberately long-lived server-sent-events
            // connection is a hard kill at that deadline, not a safety net.
            // readTimeout(0) alone would not save it: callTimeout bounds the
            // entire call including the streaming body.
            .callTimeout(0, TimeUnit.MILLISECONDS)
            .build()

    fun subscribe(): Flow<NotificationItem> = callbackFlow {
        val request = Request.Builder()
            .url("http://localhost/api/v1/notifications/stream")
            .header("Accept", "text/event-stream")
            .build()

        // Last time ANY byte arrived (open counts). With readTimeout(0) a
        // half-open socket — a NAT/router idle-drop that never sends an RST,
        // the common case on a TV left on for days — parks the read forever:
        // no exception is ever thrown, so neither the repository's `.retry`
        // nor the callers' reconnect loops can fire, and every SSE-driven
        // feature (cross-device progress, "play on this TV") stays dead until
        // the process restarts. The watchdog below turns that silence into
        // the failure the reconnect machinery is already built to handle.
        // Atomic, not @Volatile: written on OkHttp's callback thread, read on
        // the watchdog coroutine.
        val lastActivityMs = java.util.concurrent.atomic.AtomicLong(System.currentTimeMillis())

        val source: EventSource = EventSources.createFactory(sseClient)
            .newEventSource(request, object : EventSourceListener() {
                override fun onOpen(eventSource: EventSource, response: Response) {
                    lastActivityMs.set(System.currentTimeMillis())
                }

                override fun onEvent(
                    eventSource: EventSource,
                    id: String?,
                    type: String?,
                    data: String,
                ) {
                    lastActivityMs.set(System.currentTimeMillis())
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

        // Liveness watchdog. The server's own heartbeat cadence is the
        // reference: silence well past it means the socket is gone even
        // though the read never returned. Closing the flow exceptionally
        // routes into the SAME reconnect path a real network error takes.
        val watchdog = launch {
            while (isActive) {
                delay(WATCHDOG_TICK_MS)
                if (System.currentTimeMillis() - lastActivityMs.get() > STALE_AFTER_MS) {
                    close(IOException("SSE stream stalled — no data for ${STALE_AFTER_MS / 1000}s"))
                    return@launch
                }
            }
        }

        awaitClose {
            watchdog.cancel()
            source.cancel()
        }
    }

    companion object {
        private const val WATCHDOG_TICK_MS = 30_000L
        // The server emits a keepalive comment on its own cadence; this is
        // several multiples of it, so a healthy-but-quiet stream is never
        // torn down while a genuinely dead one is caught within a minute.
        private const val STALE_AFTER_MS = 150_000L
    }
}
