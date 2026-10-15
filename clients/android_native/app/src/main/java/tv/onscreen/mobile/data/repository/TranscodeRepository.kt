package tv.onscreen.mobile.data.repository

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.api.StreamLimit
import tv.onscreen.mobile.data.model.PlaybackDecision
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

    /** Server-authoritative play decision (capability profiles). The device's
     *  X-Client-Capabilities header (added by AuthInterceptor) tells the server
     *  what this device can decode. See docs/capability-profiles.md.
     *
     *  Never throws (the caller runs it alongside other loads in one scope):
     *  an admin's concurrent-stream limit (429 TOO_MANY_SESSIONS) is
     *  [PlayDecisionResult.TooManyStreams] — an answer, not a failure, so the
     *  caller must not fall back to its own decision and stream anyway — and
     *  any other failure is [PlayDecisionResult.Unavailable], on which the
     *  caller falls back to the local PlaybackHelper. */
    suspend fun decide(itemId: String, fileId: String?): PlayDecisionResult {
        return try {
            PlayDecisionResult.Decided(
                api.playbackDecision(itemId, PlaybackDecisionRequest(file_id = fileId)).data,
            )
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            StreamLimit.messageOf(e)?.let { PlayDecisionResult.TooManyStreams(it) }
                ?: PlayDecisionResult.Unavailable
        }
    }

    suspend fun stop(sessionId: String, token: String) {
        try {
            api.stopTranscode(sessionId, token)
        } catch (_: Exception) {
            // Best-effort cleanup.
        }
    }

    /** Fire-and-forget session stop on the app-lifetime [detachedScope].
     *
     *  The player's only exit hook is ViewModel.onCleared(), and androidx
     *  closes viewModelScope BEFORE calling it — a viewModelScope.launch
     *  there never runs, so the DELETE was dead code and every backed-out
     *  playback left an ffmpeg session pinned on the server until its idle
     *  reaper fired (one per episode on auto-advance). Same trap, and same
     *  remedy, as ItemRepository.reportProgressDetached. */
    fun stopDetached(sessionId: String, token: String) {
        detachedScope.launch { stop(sessionId, token) }
    }

    private val detachedScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
}

/** What POST /items/{id}/playback-decision answered (see
 *  [TranscodeRepository.decide]). */
sealed interface PlayDecisionResult {
    data class Decided(val decision: PlaybackDecision) : PlayDecisionResult

    /** 429 TOO_MANY_SESSIONS: the user's concurrent-stream limit is reached.
     *  [message] is the server's viewer-ready sentence. */
    data class TooManyStreams(val message: String) : PlayDecisionResult

    /** No answer (unreachable, an older server, a parse failure). */
    data object Unavailable : PlayDecisionResult

    /** The verdict string — "directPlay" | "directStream" | "transcode" | … —
     *  or null without one. */
    val verdict: String? get() = (this as? Decided)?.decision?.decision

    companion object {
        /** A plain verdict, as an older server answers. */
        fun of(verdict: String): PlayDecisionResult = Decided(PlaybackDecision(decision = verdict))
    }
}
