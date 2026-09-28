package tv.onscreen.mobile.data.api

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
import tv.onscreen.mobile.data.model.NotificationItem
import tv.onscreen.mobile.data.model.isHiddenNotificationType
import java.io.IOException
import java.util.concurrent.TimeUnit
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Subscribes to /api/v1/notifications/stream as an SSE source. The server
 * multiplexes user-facing notifications (item_added, scan_complete, etc.)
 * and sync events (`progress.updated`, `playback.stop`) on the same stream,
 * so subscribers receive every parsed [NotificationItem] and branch on the
 * `type` field. Reconnects are the caller's responsibility —
 * NotificationsRepository re-dials (with backoff) on the exceptional
 * completion every terminal path produces; an HTTP refusal completes with
 * [SseHttpException] so it can tell a 429 / 401 from a network drop.
 *
 * Media-request notifications (`request_*`) are dropped here, at the single
 * point every SSE row passes through: the app has no request functionality,
 * and none of those rows may surface anywhere.
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
     * SSE-tuned view of the shared client. The shared client carries a 120 s
     * callTimeout — a whole-call ceiling that, for a deliberately long-lived
     * event stream, is a hard kill every two minutes (it bounds the streaming
     * body too) — so it's cleared here. The read timeout stays, but well
     * above the server's 30 s `: keepalive` cadence (sseKeepaliveInterval):
     * a healthy-but-quiet stream always sees bytes inside the window, while a
     * half-open socket (NAT idle-drop, no RST) surfaces as a read timeout →
     * onFailure → reconnect, instead of parking the read forever. Shares the
     * parent's connection pool, interceptors and TLS config.
     */
    private val sseClient: OkHttpClient =
        client.newBuilder()
            .readTimeout(SSE_READ_TIMEOUT_S, TimeUnit.SECONDS)
            .callTimeout(0, TimeUnit.MILLISECONDS)
            .build()

    /** Parse one SSE `data` blob. Null for malformed rows and for
     *  media-request notifications, which are never surfaced. */
    internal fun parse(data: String): NotificationItem? {
        val item = try {
            adapter.fromJson(data)
        } catch (_: Exception) {
            null
        } ?: return null
        if (isHiddenNotificationType(item.type)) return null
        return item
    }

    /** [onOpen] runs (on an OkHttp thread) once the server has accepted the
     *  stream — the caller's cue that the connection is healthy again. */
    fun subscribe(onOpen: () -> Unit = {}): Flow<NotificationItem> = callbackFlow {
        val request = Request.Builder()
            .url("http://localhost/api/v1/notifications/stream")
            .header("Accept", "text/event-stream")
            .build()

        val source: EventSource = EventSources.createFactory(sseClient)
            .newEventSource(request, object : EventSourceListener() {
                override fun onOpen(eventSource: EventSource, response: Response) {
                    onOpen.invoke() // the subscribe() parameter, not this override
                }

                override fun onEvent(
                    eventSource: EventSource,
                    id: String?,
                    type: String?,
                    data: String,
                ) {
                    parse(data)?.let { trySend(it) }
                }

                // Both terminal callbacks complete the flow EXCEPTIONALLY:
                // NotificationsRepository reconnects via `retryWhen`, which
                // only fires on an exception. A normal completion would leave
                // the shared stream silently dead for the rest of the session.
                override fun onClosed(eventSource: EventSource) {
                    close(IOException("SSE stream closed by server"))
                }

                override fun onFailure(
                    eventSource: EventSource,
                    t: Throwable?,
                    response: Response?,
                ) {
                    // A non-2xx answer arrives with t == null; carry the
                    // status so the reconnect policy can back off a 429
                    // (TOO_MANY_SSE) and stop on a 401 the authenticator
                    // could not refresh past.
                    val status = response?.takeIf { !it.isSuccessful }?.code
                    close(
                        when {
                            status != null -> SseHttpException(status)
                            else -> t ?: IOException("SSE stream failed: HTTP ${response?.code ?: 0}")
                        },
                    )
                }
            })

        awaitClose { source.cancel() }
    }

    private companion object {
        /** Three keepalive intervals: never trips on a healthy stream. */
        const val SSE_READ_TIMEOUT_S = 90L
    }
}

/** The server refused the notifications stream with HTTP [code]. */
class SseHttpException(val code: Int) : IOException("SSE stream refused: HTTP $code")
