package tv.onscreen.android.ui

/**
 * Whether the activity's last stop was the screensaver: a dream (Fire TV's
 * screensaver, Google TV's Ambient Mode) starting over the app, which is now
 * waking back into it. MainActivity resets to Home after any other stop; after
 * this one it stays on the screen that was up when the screensaver came on.
 *
 * Decided from timestamps when the activity starts again, because the signals
 * come in no fixed order: ACTION_DREAMING_STARTED is a broadcast, so it can
 * land before or after the onStop the dream causes, and the stop itself waits
 * for the dream to settle (a second or two on a Fire TV Stick, up to the
 * system's 10 s idle timeout behind an animated one). By the next start all of
 * them have happened. All times are SystemClock.elapsedRealtime(). Pure,
 * JVM-tested in ScreensaverStopTest.
 */
object ScreensaverStop {

    /** How long before the dream's broadcast the stop it caused can come:
     *  the broadcast is delivered late when the main thread is busy. A HOME
     *  press can't be that close ahead of a screensaver, which only starts
     *  after minutes without input. */
    const val BROADCAST_LAG_MS = 10_000L

    /** How soon after the dream ends the activity must start again to be the
     *  screen the screensaver woke into. A dream woken with HOME ends on the
     *  launcher instead, and the app opened from there later starts on Home,
     *  as after any other HOME. */
    const val WAKE_WINDOW_MS = 10_000L

    /**
     * True when the stop at [stoppedAtMs] was a dream starting and this start,
     * at [nowMs], is the wake from it. [dreamStartedAtMs] / [dreamStoppedAtMs]
     * are the last ACTION_DREAMING_STARTED / STOPPED seen, null if none.
     */
    fun returnsToScreen(stoppedAtMs: Long, dreamStartedAtMs: Long?, dreamStoppedAtMs: Long?, nowMs: Long): Boolean {
        val started = dreamStartedAtMs ?: return false
        // This dream's end, if seen yet: a wake that starts the activity can
        // run ahead of the DREAMING_STOPPED broadcast.
        val ended = dreamStoppedAtMs?.takeIf { it >= started }
        // A stop while the dream ran is the dream's: nothing else can stop
        // the app then, since any input ends the dream first. Before it (by
        // more than the broadcast's lag) or after it, the stop was something
        // else: HOME, with the screensaver coming on over the launcher later.
        if (stoppedAtMs < started - BROADCAST_LAG_MS) return false
        if (ended != null && stoppedAtMs > ended) return false
        return ended == null || nowMs - ended <= WAKE_WINDOW_MS
    }
}
