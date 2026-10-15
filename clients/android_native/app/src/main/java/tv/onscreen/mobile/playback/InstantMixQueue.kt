package tv.onscreen.mobile.playback

import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.MixTrack

/**
 * Hands an instant mix (GET /music/mix, v2.6) from the page that asked for it
 * to the now-playing screen that starts it.
 *
 * The mix plays through the ordinary path: the page opens the player on the
 * mix's first track, and when the screen hands that track to the background
 * service it finds the rest of the mix here and queues it behind, as one
 * playlist (gapless, lazily resolved — see [MusicQueue]). Its entries are
 * marked so the service doesn't swap the mix for the first track's album or
 * run on into albums after it.
 *
 * Process-wide (the page and the player share no object graph) and
 * single-slot: the newest mix wins. Expires quickly, so a mix whose player
 * never started (an error, a transcode the service can't take) can't hijack
 * a later play of the same track. Pure apart from the clock, which callers
 * pass in.
 */
object InstantMixQueue {

    private const val TTL_MS = 30_000L

    /** One queued track: what a placeholder entry needs. */
    data class Entry(val id: String, val title: String, val albumId: String, val index: Int?) {
        fun asChild(): ChildItem = ChildItem(id = id, title = title, type = MusicQueue.TRACK, index = index)
    }

    private class Pending(val firstId: String, val rest: List<Entry>, val atMs: Long)

    @Volatile private var pending: Pending? = null

    /**
     * Park [tracks] (the mix, in play order; repeats dropped) and return the
     * id to open the player on — the first track — or null for an empty mix.
     */
    fun put(tracks: List<MixTrack>, nowMs: Long): String? {
        val unique = tracks.filter { it.id.isNotBlank() }.distinctBy { it.id }
        val first = unique.firstOrNull() ?: return null
        pending = Pending(
            firstId = first.id,
            rest = unique.drop(1).map { Entry(it.id, it.title, it.album_id, it.index) },
            atMs = nowMs,
        )
        return first.id
    }

    /** The rest of the mix that starts at [firstId] — once — or null when
     *  none is waiting for it. */
    fun take(firstId: String, nowMs: Long): List<Entry>? {
        val p = pending ?: return null
        if (p.firstId != firstId) return null
        pending = null
        return p.rest.takeIf { nowMs - p.atMs in 0..TTL_MS }
    }

    /** Drop whatever is waiting (tests; sign-out). */
    fun clear() {
        pending = null
    }
}
