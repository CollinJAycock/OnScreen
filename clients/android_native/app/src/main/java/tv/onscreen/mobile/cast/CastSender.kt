package tv.onscreen.mobile.cast

import android.content.Context
import android.net.Uri
import com.google.android.gms.cast.MediaInfo
import com.google.android.gms.cast.MediaLoadRequestData
import com.google.android.gms.cast.MediaMetadata
import com.google.android.gms.cast.framework.CastContext
import com.google.android.gms.common.images.WebImage
import org.json.JSONObject

/**
 * Android-side glue for sending a [CastMediaInfo.Payload] to a connected
 * Cast receiver. The pure payload assembly lives in [CastMediaInfo]
 * (unit-tested); this file only translates that to Cast SDK types and
 * fires the LOAD.
 *
 * Caller flow:
 *   1. User taps the MediaRouteButton, picks a device, framework opens
 *      the session — this side does nothing.
 *   2. User taps "Cast to TV" on our side. We grab the current
 *      CastSession (must already exist), build a Payload via
 *      CastMediaInfo.build, translate to MediaLoadRequestData, fire.
 *   3. Local ExoPlayer pauses (caller responsibility).
 */
object CastSender {

    /** True when there's a connected Cast session right now. UI binds
     *  to this to enable / disable the "Cast to TV" button. */
    fun isConnected(context: Context): Boolean {
        return try {
            CastContext.getSharedInstance(context.applicationContext)
                .sessionManager
                .currentCastSession
                ?.isConnected == true
        } catch (_: Throwable) {
            // Cast SDK not initialised on this device (Play Services
            // missing, etc.) — treat as not-connected; the
            // MediaRouteButton itself surfaces the unavailable state.
            false
        }
    }

    /**
     * Send a LOAD request to the active session. Returns true if the
     * SDK accepted it; false if there's no active session, the
     * payload couldn't be built, or the SDK rejected the request.
     *
     * The caller is responsible for pausing the local player — we
     * don't reach into the ExoPlayer instance from here.
     */
    fun load(context: Context, payload: CastMediaInfo.Payload): Boolean {
        val session = try {
            CastContext.getSharedInstance(context.applicationContext)
                .sessionManager
                .currentCastSession
        } catch (_: Throwable) {
            null
        }
        val client = session?.remoteMediaClient ?: return false

        val metadata = MediaMetadata(payload.metadataType).apply {
            putString(MediaMetadata.KEY_TITLE, payload.title)
            payload.subtitle?.let { putString(MediaMetadata.KEY_SUBTITLE, it) }
            payload.imageUrls.forEach { addImage(WebImage(Uri.parse(it))) }
        }

        val info = MediaInfo.Builder(payload.contentId)
            .setStreamType(MediaInfo.STREAM_TYPE_BUFFERED)
            .setContentType(payload.contentType)
            .setMetadata(metadata)
            .apply {
                payload.durationSeconds?.let {
                    // Cast wants ms; Payload carries seconds.
                    setStreamDuration(it * 1000L)
                }
                if (payload.customData.isNotEmpty()) {
                    setCustomData(JSONObject(payload.customData as Map<*, *>))
                }
            }
            .build()

        val request = MediaLoadRequestData.Builder()
            .setMediaInfo(info)
            .build()

        return try {
            client.load(request)
            true
        } catch (_: Throwable) {
            false
        }
    }
}
