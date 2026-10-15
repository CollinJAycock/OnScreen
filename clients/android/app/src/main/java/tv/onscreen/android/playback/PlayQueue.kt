package tv.onscreen.android.playback

import tv.onscreen.android.data.model.ChildItem

/**
 * The tracks an instant mix (v2.6, GET /music/mix) plays, in order. The
 * player has no queue of its own: what follows an item is found by
 * NextSiblingResolver — for the fragment's chain and the background
 * service's alike — which asks here first. While the playing item is in the
 * queue, the next one is the queue's; after the queue's last, nothing plays
 * (no running on into that track's album).
 *
 * Process-wide, like AudioHandoff. A playback started any other way ends the
 * mix: DetailFragment and Navigator clear it when the user starts something,
 * and the player keeps it only for an item it holds ([retainFor]).
 */
object PlayQueue {

    @Volatile
    private var tracks: List<ChildItem> = emptyList()

    /** Make [queue] the playing queue (empty clears it). */
    fun set(queue: List<ChildItem>) {
        tracks = queue.distinctBy { it.id }
    }

    fun clear() {
        tracks = emptyList()
    }

    /** Whether [itemId] is one of the queue's tracks. */
    fun contains(itemId: String): Boolean = tracks.any { it.id == itemId }

    /** The track after [itemId]; null after the last, or when [itemId] isn't
     *  in the queue. */
    fun nextAfter(itemId: String): ChildItem? {
        val q = tracks
        val i = q.indexOfFirst { it.id == itemId }
        return if (i < 0) null else q.getOrNull(i + 1)
    }

    /** The player started [itemId]: keep the queue only if it holds it (a
     *  playback that isn't the mix's ends the mix). */
    fun retainFor(itemId: String) {
        if (!contains(itemId)) clear()
    }
}
