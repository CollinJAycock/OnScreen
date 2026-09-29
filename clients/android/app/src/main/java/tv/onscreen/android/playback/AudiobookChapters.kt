package tv.onscreen.android.playback

import tv.onscreen.android.data.model.ChildItem

/**
 * Chapter order for a multi-file audiobook (a book whose chapters are
 * separate `audiobook_chapter` items). Pure, JVM-tested in
 * AudiobookChaptersTest. Mirrors the phone client's AudiobookChapters.
 *
 * The order is the book's /children listing: numbered chapters by number,
 * then unnumbered ones as the server lists them (by title). The scanner
 * numbers chapter files from their names or track tags, but one it can't
 * number stays unnumbered, so chapters can't be matched on `index + 1` the
 * way [NextSiblingResolver.nextInContainer] steps through tracks. Same
 * order as the book page's Play button (DetailFragment, [ChildItem.PLAY_ORDER]).
 */
object AudiobookChapters {

    /** [children]'s chapters in listening order. */
    fun inOrder(children: List<ChildItem>): List<ChildItem> =
        children
            .filter { it.type == AudiobookSpeed.CHAPTER }
            .distinctBy { it.id }
            // sortedWith is stable: unnumbered chapters keep the server's order.
            .sortedWith(ChildItem.PLAY_ORDER)

    /** The chapter after [currentId], or null at the end of the book (or
     *  when [currentId] is no longer listed). Never another book's: the
     *  listing is one book's children. */
    fun nextAfter(children: List<ChildItem>, currentId: String): ChildItem? {
        val ordered = inOrder(children)
        val at = ordered.indexOfFirst { it.id == currentId }
        if (at < 0) return null
        return ordered.getOrNull(at + 1)
    }
}
