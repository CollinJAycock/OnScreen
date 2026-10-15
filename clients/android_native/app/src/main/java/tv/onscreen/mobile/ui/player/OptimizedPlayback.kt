package tv.onscreen.mobile.ui.player

import tv.onscreen.mobile.data.model.ItemFile
import tv.onscreen.mobile.data.model.OptimizedVersion
import tv.onscreen.mobile.data.model.PlaybackDecision

/**
 * Optimized versions (v2.6, docs/capability-profiles.md §13). An admin can
 * have the server encode an item ahead of time into a universally playable
 * MP4 (H.264 + stereo AAC). This client declares `optimized=1` in
 * X-Client-Capabilities; when the source would need a video transcode and a
 * version fits, the play decision answers `decision: directPlay`,
 * `reason: optimized_version`, with the version to play and the source's own
 * verdict (`source_decision`) for a fallback.
 *
 * The version plays as a direct play of its stream_url, authenticated the way
 * file streams are (its stream_token in the URL, through StreamTokenVault).
 * Progress and scrobbles stay the item's. If it won't play, the player falls
 * back ONCE to the source's verdict (PlayerViewModel.fallBackFromOptimized).
 */
object OptimizedPlayback {

    const val REASON = "optimized_version"

    /** Every optimized version is an MP4. */
    const val CONTAINER = "mp4"

    /** The version [decision] says to play, or null when it says to play the
     *  source (or there is no decision). */
    fun versionToPlay(decision: PlaybackDecision?): OptimizedVersion? =
        decision
            ?.takeIf { it.reason == REASON && it.decision == "directPlay" }
            ?.optimized_version
            ?.takeIf { it.stream_url.isNotBlank() }

    /** How to play [file] when its optimized version failed: the source's own
     *  verdict, which the server sent alongside the version. */
    fun fallbackMode(decision: PlaybackDecision, file: ItemFile): PlaybackMode =
        PlaybackHelper.modeFor(decision.source_decision, file)

    /** The source itself can't be streamed (Dolby Vision: "unsupported"), so
     *  there is nothing to fall back to. */
    fun sourceUnplayable(decision: PlaybackDecision): Boolean =
        decision.source_decision == "unsupported" || decision.source_decision == "damaged"
}
