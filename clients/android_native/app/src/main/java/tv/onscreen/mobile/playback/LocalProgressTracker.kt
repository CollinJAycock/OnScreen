package tv.onscreen.mobile.playback

/**
 * Last progress position THIS device published, per item.
 *
 * The server broadcasts every progress write to all of the user's connected
 * devices, so the cross-device resume listener has to tell "another device
 * moved" from "our own write coming back". PlayerViewModel used to do that
 * against a field only IT wrote — which left [PlaybackService] invisible:
 * background audio heartbeats every 10 s echoed back, looked like a remote
 * device, and the now-playing screen seeked the player to the position it
 * had just reported. Whatever the user did in between (a scrub, a skip) was
 * undone on the next tick, once per heartbeat, for as long as the screen
 * stayed open.
 *
 * Both writers are in the same process, so a plain object is enough. Keyed
 * by item so a stale entry for a previous item can't suppress a genuine
 * remote resume for the current one.
 */
object LocalProgressTracker {

    private val lock = Any()
    private var itemId: String? = null
    private var positionMs: Long = 0L

    /** Record a position this device just reported for [item]. */
    fun record(item: String, position: Long) {
        synchronized(lock) {
            itemId = item
            positionMs = position
        }
    }

    /** Last position this device reported for [item], or null if the last
     *  write was for a different item (or nothing has been written yet). */
    fun lastFor(item: String): Long? = synchronized(lock) {
        if (itemId == item) positionMs else null
    }
}
