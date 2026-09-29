package tv.onscreen.android.playback

import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.repository.ItemRepository

/** Stateless lookup helper that finds the next item to play after a
 *  given track, episode or audiobook chapter.
 *
 *  Pulls in two scopes: in-container next sibling (S04E12 → S04E13,
 *  album track 5 → track 6, a book's chapter 3 → chapter 4) and
 *  cross-container fall-through (S04E12 last → S05E01, last track of
 *  album A → first track of album B). A book's last chapter has no
 *  fall-through: a book never runs on into the next one. Movies and
 *  standalone audio return null — auto-advance isn't a thing for them.
 *
 *  Lives outside the ViewModel so the MediaSessionService can call
 *  it from a Player.Listener when the service-owned ExoPlayer hits
 *  STATE_ENDED. Same logic the fragment-side viewmodel uses, but
 *  reachable from background code with no Compose / Lifecycle
 *  dependencies. */
class NextSiblingResolver(private val itemRepo: ItemRepository) {

    /** Resolve the item that should follow [currentItemId] given its
     *  type, parent, and 1-based index. Returns null when there's no
     *  next item in the catalog (last episode of last season, last
     *  track of last album, last chapter of a book, anything that isn't
     *  a track, episode, chapter or numbered book in a series).
     */
    suspend fun resolve(
        currentItemId: String,
        type: String,
        parentId: String?,
        currentIndex: Int?,
    ): ChildItem? {
        if (parentId == null) return null
        // A chapter needs no index: an unnumbered one still has a place in
        // its book's listing.
        if (type == AudiobookSpeed.CHAPTER) return nextChapter(currentItemId, parentId)
        if (currentIndex == null) return null
        return try {
            val children = itemRepo.getChildren(parentId)
            val next = nextInContainer(children, currentItemId, type, currentIndex)
            if (next != null) return next

            // Cross-container fall-through. Same shape for tracks
            // and episodes; only the container type and sort order
            // differ.
            if (type != "track" && type != "episode") return null
            val parent = itemRepo.getItem(parentId)
            val grandparentId = parent.parent_id ?: return null
            val containerType = if (type == "track") "album" else "season"
            val rawSiblings = itemRepo.getChildren(grandparentId)
                .filter { it.type == containerType }
            val siblings = if (type == "track") {
                rawSiblings.sortedWith(
                    compareBy({ it.year ?: Int.MAX_VALUE }, { it.index ?: Int.MAX_VALUE }),
                )
            } else {
                rawSiblings.sortedBy { it.index ?: Int.MAX_VALUE }
            }
            val currentIdx = siblings.indexOfFirst { it.id == parentId }
            if (currentIdx < 0) return null
            val nextContainer = siblings.getOrNull(currentIdx + 1) ?: return null
            itemRepo.getChildren(nextContainer.id)
                .filter { it.type == type && it.index != null }
                .sortedWith(ChildItem.PLAY_ORDER)
                .firstOrNull()
        } catch (_: Exception) {
            null
        }
    }

    /** The chapter of [bookId] after [chapterId], by the book's listing
     *  ([AudiobookChapters]). Null after the last one. */
    private suspend fun nextChapter(chapterId: String, bookId: String): ChildItem? =
        try {
            AudiobookChapters.nextAfter(itemRepo.getChildren(bookId), chapterId)
        } catch (_: Exception) {
            null
        }

    companion object {
        /** The [type] row of [children] that follows [currentItemId]: the
         *  nearest one after it in (disc, index) order.
         *
         *  Next-GREATER, not exactly currentIndex + 1. A library missing one
         *  file (episode 4 of 10 absent, a track ripped out of an album)
         *  leaves a numbering gap; an exact-successor match finds nothing
         *  there and falls through to the cross-container branch, which jumps
         *  to the next season / album entirely. Taking the next greater
         *  position steps over the gap and keeps playing in place.
         *
         *  The disc matters because a multi-disc album restarts its numbering
         *  on each disc: by index alone, disc 2 track 5 is followed by disc 1
         *  track 6, and the last track of disc 1 by nothing on disc 2. The
         *  current row's disc comes from [children] (the item endpoint doesn't
         *  carry one); without one it is disc 1, as every episode is. */
        fun nextInContainer(
            children: List<ChildItem>,
            currentItemId: String,
            type: String,
            currentIndex: Int,
        ): ChildItem? {
            val disc = children.firstOrNull { it.id == currentItemId }?.disc_number ?: 1
            return children
                .filter { it.type == type && it.index != null }
                .sortedWith(ChildItem.PLAY_ORDER)
                .firstOrNull {
                    val d = it.disc_number ?: 1
                    d > disc || (d == disc && (it.index ?: Int.MIN_VALUE) > currentIndex)
                }
        }
    }
}
