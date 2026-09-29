package tv.onscreen.mobile.ui.player

import tv.onscreen.mobile.data.model.Chapter

/**
 * Sleep-timer modes the player offers from the bottom-sheet picker.
 *
 * - [Off] is the unset state — the user has no countdown running.
 * - [Minutes] is a wall-clock countdown; player pauses when it hits 0
 *   regardless of where in the track they are.
 * - [EndOfTrack] is a content-aware mode: don't pause until the
 *   currently-playing track / episode finishes, then stop. Useful for
 *   "let me hear this album side then sleep".
 * - [EndOfChapter] is the audiobook form of it: stop at the end of the
 *   chapter being heard. A single-file book stops at its next embedded
 *   chapter mark ([SleepTimerMath.chapterEndMs]); a multi-file book, whose
 *   chapters are separate items, when the chapter item ends.
 *
 * Pure data — no Android imports. Tick logic + pause-trigger live in
 * the VM; this file just defines the modes + the deterministic
 * countdown-arithmetic helper that's worth testing in isolation.
 */
sealed class SleepTimer {
    data object Off : SleepTimer()
    data class Minutes(val total: Int) : SleepTimer()
    data object EndOfTrack : SleepTimer()
    data object EndOfChapter : SleepTimer()
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

/**
 * A running timer on its way from one now-playing screen to the next. When
 * the background queue moves on (a multi-file book chaining to its next
 * chapter, a skip from the lock screen), the screen re-opens on the new item
 * with a fresh ViewModel — and the timer, which lived in the old one, was
 * simply gone. The old screen [put]s it here for the item it is following
 * to; the new one [take]s it. A countdown loses the time spent in between.
 * Expires quickly, like the follow mark it travels with, so an abandoned
 * hand-off can't arm a timer on some later, unrelated playback.
 */
object SleepTimerCarry {
    private const val TTL_MS = 10_000L

    private data class Carried(val itemId: String, val state: SleepTimerState, val atMs: Long)

    @Volatile private var carried: Carried? = null

    fun put(itemId: String, mode: SleepTimer, remainingMs: Long, nowMs: Long) {
        carried = Carried(itemId, SleepTimerState(mode, remainingMs), nowMs)
    }

    /** The timer handed to [itemId] (a countdown with the time since the
     *  hand-off taken off), or null. One-shot. */
    @Synchronized
    fun take(itemId: String, nowMs: Long): SleepTimerState? {
        val c = carried ?: return null
        if (c.itemId != itemId) return null
        carried = null
        val elapsed = (nowMs - c.atMs).coerceAtLeast(0L)
        if (elapsed > TTL_MS) return null
        return if (c.state.mode is SleepTimer.Minutes) {
            c.state.copy(remainingMs = c.state.remainingMs - elapsed)
        } else {
            c.state
        }
    }
}

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
        SleepTimer.EndOfChapter -> 0L
        SleepTimer.Off -> 0L
    }

    /** Whether [mode] waits for the playing item itself to end (the
     *  STATE_ENDED path) — true for both content-aware modes, since a
     *  multi-file book's chapter IS the item. */
    fun endsWithItem(mode: SleepTimer?): Boolean =
        mode == SleepTimer.EndOfTrack || mode == SleepTimer.EndOfChapter

    /**
     * Where "end of chapter" falls for a single-file book playing at
     * [positionMs] (content time): the start of the next embedded chapter
     * after it — or, in the last chapter, that chapter's end when it stops
     * short of the file's. Null when there's no later chapter boundary: the
     * chapter runs to the end of the item, and the timer stops there (a
     * multi-file book's chapters always do; so does a file with no chapter
     * marks).
     *
     * Strictly after [positionMs], so arming the timer a moment after a
     * chapter starts (or right after jumping to one) waits for THAT
     * chapter's end, not the one just crossed.
     */
    fun chapterEndMs(chapters: List<Chapter>, positionMs: Long): Long? {
        if (chapters.isEmpty()) return null
        val nextStart = chapters.asSequence()
            .map { it.start_ms }
            .filter { it > positionMs }
            .minOrNull()
        if (nextStart != null) return nextStart
        // Past the last chapter's start: its own end, if it ends before the
        // file does (the item's end would otherwise come first anyway).
        val lastEnd = chapters.maxOf { it.end_ms }
        return lastEnd.takeIf { it > positionMs }
    }

    /** How long to wait before looking again, heading for [targetMs] from
     *  [positionMs] at [speed]: the wall-clock time left, capped to a second
     *  (so a seek is noticed quickly) and floored so a stalled player isn't
     *  polled in a tight loop. */
    fun nextCheckDelayMs(targetMs: Long?, positionMs: Long, speed: Float): Long {
        if (targetMs == null) return 1_000L
        val safeSpeed = if (speed.isFinite() && speed > 0f) speed else 1f
        val wallMs = ((targetMs - positionMs) / safeSpeed).toLong()
        return wallMs.coerceIn(50L, 1_000L)
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
