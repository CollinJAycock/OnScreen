package tv.onscreen.android.data.repository

import retrofit2.HttpException
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.api.StreamLimit
import tv.onscreen.android.data.api.TooManyStreamsException
import tv.onscreen.android.data.model.PlaybackDecision
import tv.onscreen.android.data.model.PlaybackDecisionRequest
import tv.onscreen.android.data.model.TranscodeRequest
import tv.onscreen.android.data.model.TranscodeSession
import tv.onscreen.android.playback.StreamSession
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
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
        supportsAv1: Boolean = false,
        burnSubtitleStreamIndex: Int? = null,
    ): TranscodeSession {
        return try {
            api.startTranscode(
                itemId,
                TranscodeRequest(
                    file_id = fileId,
                    height = height,
                    position_ms = positionMs,
                    video_copy = videoCopy,
                    audio_stream_index = audioStreamIndex,
                    supports_hevc = supportsHevc,
                    supports_av1 = supportsAv1,
                    burn_subtitle_stream_index = burnSubtitleStreamIndex,
                )
            ).data
        } catch (e: HttpException) {
            // Over the user's concurrent-stream limit: a TooManyStreamsException
            // carrying the server's sentence (StreamLimit).
            throw StreamLimit.of(e) ?: e
        }
    }

    /** Server-authoritative play decision (capability profiles): the verdict
     *  ("directPlay" | "directStream" | "transcode" | "unsupported"), the file
     *  it is for, and an optimized version to play instead when the server
     *  picked one. Null on any failure so the caller falls back to the local
     *  PlaybackHelper — except the concurrent-stream limit, which throws
     *  [TooManyStreamsException]: that refusal must stop playback, not be
     *  routed around by a local decision. The device's X-Client-Capabilities
     *  header (added by AuthInterceptor) tells the server what this device can
     *  decode. See docs/capability-profiles.md. */
    suspend fun decide(itemId: String, fileId: String?): PlaybackDecision? {
        return try {
            api.playbackDecision(itemId, PlaybackDecisionRequest(file_id = fileId)).data
        } catch (e: HttpException) {
            StreamLimit.of(e)?.let { throw it }
            null
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
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

    /** [stop] for a session whose player is gone, fired and forgotten on a
     *  scope that outlives the caller: a service being destroyed, a player
     *  evicted from the handoff slot. */
    fun stopDetached(session: StreamSession) {
        detached.launch { stop(session.id, session.token) }
    }

    private val detached = CoroutineScope(SupervisorJob() + Dispatchers.IO)
}
