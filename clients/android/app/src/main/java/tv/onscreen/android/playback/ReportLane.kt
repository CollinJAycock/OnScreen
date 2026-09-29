package tv.onscreen.android.playback

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/**
 * Sends reports on [scope] one after another, in the order they were asked
 * for: each waits until the one before it has finished.
 *
 * Progress reports for an item must reach the server in order. Launched side
 * by side, a teardown's 'paused' and 'stopped' raced each other, and a
 * 'paused' that landed second put the item back in the server's Now Playing
 * after it had stopped.
 */
class ReportLane(private val scope: CoroutineScope) {
    private var last: Job? = null

    @Synchronized
    fun launch(report: suspend () -> Unit): Job {
        val before = last
        return scope.launch {
            // join() returns however the one before ended (sent, failed or
            // cancelled), so a failed report never holds up the next.
            before?.join()
            report()
        }.also { last = it }
    }
}
