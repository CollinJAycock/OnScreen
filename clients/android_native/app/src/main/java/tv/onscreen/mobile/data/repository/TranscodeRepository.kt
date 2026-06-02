package tv.onscreen.mobile.data.repository

import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.PlaybackDecisionRequest
import tv.onscreen.mobile.data.model.TranscodeRequest
import tv.onscreen.mobile.data.model.TranscodeSession
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class TranscodeRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    suspend fun start(
        itemId: String,
        height: Int,
        positionMs: Long,
        fileId: String? = null,
        videoCopy: Boolean = false,
        audioStreamIndex: Int? = null,
        supportsHevc: Boolean = true,
    ): TranscodeSession {
        return api.startTranscode(
            itemId,
            TranscodeRequest(
                file_id = fileId,
                height = height,
                position_ms = positionMs,
                video_copy = videoCopy,
                audio_stream_index = audioStreamIndex,
                supports_hevc = supportsHevc,
            )
        ).data
    }

    /** Server-authoritative play decision (capability profiles). Returns
     *  "directPlay" | "directStream" | "transcode", or null on any failure so
     *  the caller falls back to the local PlaybackHelper. The device's
     *  X-Client-Capabilities header (added by AuthInterceptor) tells the server
     *  what this device can decode. See docs/capability-profiles.md. */
    suspend fun decide(itemId: String, fileId: String?): String? {
        return try {
            api.playbackDecision(itemId, PlaybackDecisionRequest(file_id = fileId)).data.decision
        } catch (_: Exception) {
            null
        }
    }

    suspend fun stop(sessionId: String, token: String) {
        try {
            api.stopTranscode(sessionId, token)
        } catch (_: Exception) {
            // Best-effort cleanup.
        }
    }
}
