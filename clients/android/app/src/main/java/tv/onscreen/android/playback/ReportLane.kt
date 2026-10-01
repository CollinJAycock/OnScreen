package tv.onscreen.android.playback

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/**
 * Sends reports on [scope] one after another, in the order they were asked
 * for: each waits until the ones before it have finished.
 *
 * Progress reports for an item must reach the server in order. Launched side
 * by side, a teardown's 'paused' and 'stopped' raced each other, and a
 * 'paused' that landed second put the item back in the server's Now Playing
 * after it had stopped.
 *
 * A report cancelled (its Job) before its turn comes is never sent, but
 * keeps its place: the ones behind it still wait for the ones before it.
 */
class ReportLane(private val scope: CoroutineScope) {
    /** Done once every report asked for so far has finished. */
    private var settled: Job? = null

    @Synchronized
    fun launch(report: suspend () -> Unit): Job {
        val before = settled
        val job = scope.launch {
            // join() returns however the ones before ended (sent, failed or
            // cancelled), so a failed report never holds up the next.
            before?.join()
            report()
        }
        // The next report waits for this one AND the ones before it. A
        // report cancelled while it waited its turn ends at once, and the
        // ones behind it, waiting on it alone, went out ahead of the one it
        // was waiting for.
        settled = scope.launch {
            job.join()
            before?.join()
        }
        return job
    }
}
