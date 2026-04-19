package tv.onscreen.android.data.repository

import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.TranscodeRequest
import tv.onscreen.android.data.model.TranscodeSession
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

    suspend fun stop(sessionId: String, token: String) {
        try {
            api.stopTranscode(sessionId, token)
        } catch (_: Exception) {
            // Best-effort cleanup.
        }
    }
}
