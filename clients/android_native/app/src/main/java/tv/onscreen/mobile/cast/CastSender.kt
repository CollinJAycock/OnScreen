package tv.onscreen.mobile.cast

import android.content.Context
import android.net.Uri
import com.google.android.gms.cast.HlsSegmentFormat
import com.google.android.gms.cast.HlsVideoSegmentFormat
import com.google.android.gms.cast.MediaInfo
import com.google.android.gms.cast.MediaLoadRequestData
import com.google.android.gms.cast.MediaMetadata
import com.google.android.gms.cast.MediaTrack
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
    fun load(
        context: Context,
        payload: CastMediaInfo.Payload,
        /** The receiver's answer to the LOAD: true once it is playing it,
         *  false when it failed (a media error, an unsupported stream).
         *  Called on the main thread; not called when this returns false. */
        onResult: ((Boolean) -> Unit)? = null,
    ): Boolean {
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
                // An OnScreen HLS session: the DMR must be told the segment
                // container up front — it assumes MPEG-TS with AAC, and an
                // fMP4 (HEVC / AV1) session then fails to demux.
                payload.hlsSegmentFormat?.let { fmt ->
                    when (fmt) {
                        CastHls.SegmentFormat.TS -> {
                            setHlsSegmentFormat(HlsSegmentFormat.TS)
                            if (payload.hasVideo) setHlsVideoSegmentFormat(HlsVideoSegmentFormat.MPEG2_TS)
                        }
                        CastHls.SegmentFormat.FMP4 -> {
                            setHlsSegmentFormat(HlsSegmentFormat.FMP4)
                            if (payload.hasVideo) setHlsVideoSegmentFormat(HlsVideoSegmentFormat.FMP4)
                        }
                    }
                }
                if (payload.textTracks.isNotEmpty()) {
                    setMediaTracks(
                        payload.textTracks.map { t ->
                            MediaTrack.Builder(t.id, MediaTrack.TYPE_TEXT)
                                .setSubtype(MediaTrack.SUBTYPE_SUBTITLES)
                                .setContentId(t.url)
                                .setContentType("text/vtt")
                                .apply {
                                    if (t.language.isNotBlank()) setLanguage(t.language)
                                    if (t.name.isNotBlank()) setName(t.name)
                                }
                                .build()
                        },
                    )
                }
            }
            .build()

        val request = MediaLoadRequestData.Builder()
            .setMediaInfo(info)
            // WHY: without a start position the receiver plays from 0:00,
            // dropping the local playhead on hand-off. Payload carries a
            // content-absolute position in SECONDS; the Cast Android SDK's
            // setCurrentTime takes MILLISECONDS (same unit as the sibling
            // setStreamDuration above, and unlike the web chrome.cast SDK
            // whose currentTime is seconds), so convert here.
            .setCurrentTime((payload.positionSeconds * 1000).toLong())
            .setAutoplay(true)
            .apply {
                payload.textTracks.firstOrNull()?.let { setActiveTrackIds(longArrayOf(it.id)) }
            }
            .build()

        return try {
            val pending = client.load(request)
            if (onResult != null) {
                pending.setResultCallback { result -> onResult(result.status.isSuccess) }
            }
            true
        } catch (_: Throwable) {
            false
        }
    }
}
