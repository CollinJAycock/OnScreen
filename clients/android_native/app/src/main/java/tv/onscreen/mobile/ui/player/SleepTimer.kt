package tv.onscreen.mobile.ui.player

/**
 * Sleep-timer modes the player offers from the bottom-sheet picker.
 *
 * - [Off] is the unset state — the user has no countdown running.
 * - [Minutes] is a wall-clock countdown; player pauses when it hits 0
 *   regardless of where in the track they are.
 * - [EndOfTrack] is a content-aware mode: don't pause until the
 *   currently-playing track / episode finishes, then stop. Useful for
 *   "let me hear this album side then sleep".
 *
 * Pure data — no Android imports. Tick logic + pause-trigger live in
 * the VM; this file just defines the modes + the deterministic
 * countdown-arithmetic helper that's worth testing in isolation.
 */
sealed class SleepTimer {
    data object Off : SleepTimer()
    data class Minutes(val total: Int) : SleepTimer()
    data object EndOfTrack : SleepTimer()
}

/**
 * Snapshot of an active timer. UI binds to this to render the
 * countdown chip. Null = no timer running.
 *
 * remainingMs goes negative briefly when the tick fires before the VM
 * reacts; clamp at 0 in the renderer rather than asserting positivity
 * here so we can drop the constraint cheaply.
 */
data class SleepTimerState(
    val mode: SleepTimer,
    val remainingMs: Long,
)

object SleepTimerMath {

    /** Five quick-pick options the player UI surfaces. Five is the
     *  point past which a horizontal row stops fitting on a phone in
     *  portrait without overflowing — bigger pickers should adopt a
     *  bottom-sheet wheel instead. */
    val QUICK_PICKS_MIN: List<Int> = listOf(15, 30, 45, 60, 90)

    /** Convert a [SleepTimer.Minutes] mode to the initial countdown
     *  in milliseconds. EndOfTrack and Off both return 0 — those modes
     *  don't run a wall-clock countdown. */
    fun initialMs(mode: SleepTimer): Long = when (mode) {
        is SleepTimer.Minutes -> mode.total.toLong() * 60L * 1000L
        SleepTimer.EndOfTrack -> 0L
        SleepTimer.Off -> 0L
    }

    /** Given a remaining-ms value, format it as `MM:SS`. Negative
     *  inputs clamp to 0 so the UI never shows `-0:01` between the
     *  tick and the pause action. */
    fun formatRemaining(ms: Long): String {
        val clamped = if (ms < 0) 0L else ms
        val total = clamped / 1000L
        val m = total / 60L
        val s = total % 60L
        return "%d:%02d".format(m, s)
    }

    /** Decide whether a tick at [now] reached the cutoff. The VM
     *  passes elapsed time since timer start; this returns true once
     *  remaining ≤ 0 so the VM fires the pause action exactly once. */
    fun isExpired(remainingMs: Long): Boolean = remainingMs <= 0L
}
