package tv.onscreen.mobile.playback

import tv.onscreen.mobile.data.model.ChildItem

/**
 * Chapter order for a multi-file audiobook (a book whose chapters are
 * separate `audiobook_chapter` items). Pure, JVM-tested in
 * AudiobookChaptersTest.
 *
 * Not [NextSiblingResolver]: that walks by index, and the scanner doesn't
 * number chapter files, so a chapter usually has none and the resolver
 * finds nothing. The book's /children listing is the order to follow —
 * numbered chapters by number, unnumbered ones after them in the order the
 * server lists them (the web book page sorts the same way).
 */
object AudiobookChapters {

    /** [children]'s chapters in listening order. */
    fun inOrder(children: List<ChildItem>): List<ChildItem> =
        children
            .filter { it.type == AudiobookSpeed.CHAPTER }
            .distinctBy { it.id }
            // sortedBy is stable: unnumbered chapters keep the server's order.
            .sortedBy { it.index ?: Int.MAX_VALUE }

    /** The chapter after [currentId], or null at the end of the book (or
     *  when [currentId] is no longer listed). */
    fun nextAfter(children: List<ChildItem>, currentId: String): ChildItem? {
        val ordered = inOrder(children)
        val at = ordered.indexOfFirst { it.id == currentId }
        if (at < 0) return null
        return ordered.getOrNull(at + 1)
    }
}

/**
 * The sleep timer's "stop when this item ends" request, from the now-playing
 * screen to [PlaybackService]. Process-wide because the two share a process
 * but no object graph (the screen reaches the service only through a
 * MediaController) — the same reason [BackgroundAudioEvents] exists, in the
 * other direction.
 *
 * Why the service has to know: at STATE_ENDED it chains an audiobook to what
 * comes next (the next chapter file, or the next book) and starts it
 * playing. The screen's own pause lands on the ended item a moment before
 * that chain resolves, so "sleep at the end of this chapter" would pause
 * nothing and the next chapter would play on. Armed, the service ends there
 * instead of chaining.
 */
object StopAfterItem {
    @Volatile private var armedFor: String? = null

    /** Stop playback when [itemId] ends, instead of chaining past it. */
    fun arm(itemId: String) {
        armedFor = itemId
    }

    /** Cancel the request — only if it is still [itemId]'s, so a screen
     *  being torn down can't cancel the one its successor armed. */
    @Synchronized
    fun disarm(itemId: String) {
        if (armedFor == itemId) armedFor = null
    }

    fun isArmedFor(itemId: String): Boolean = armedFor == itemId

    /** True, once, when a stop was armed for [itemId] (then disarmed). */
    @Synchronized
    fun consume(itemId: String): Boolean {
        if (armedFor != itemId) return false
        armedFor = null
        return true
    }
}
