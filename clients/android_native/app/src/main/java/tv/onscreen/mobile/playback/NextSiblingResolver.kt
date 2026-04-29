package tv.onscreen.mobile.playback

import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.repository.ItemRepository

/** Stateless lookup helper that finds the next item to play after a
 *  given track or episode. Same logic as the TV client's resolver —
 *  in-container next sibling first (S04E12 → S04E13, album track 5
 *  → track 6), then cross-container fall-through (last episode of
 *  S04 → S05E01, last track of album A → first track of album B).
 *
 *  Lives outside the ViewModel so the [OnScreenMediaSessionService]
 *  can call it from a Player.Listener when the service-owned
 *  ExoPlayer hits STATE_ENDED — same logic the screen-side
 *  PlayerViewModel.loadNextSibling uses, but reachable from
 *  background code with no Compose / Lifecycle dependencies. */
class NextSiblingResolver(private val itemRepo: ItemRepository) {

    suspend fun resolve(
        currentItemId: String,
        type: String,
        parentId: String?,
        currentIndex: Int?,
    ): ChildItem? {
        if (parentId == null || currentIndex == null) return null
        return try {
            val children = itemRepo.getChildren(parentId)
            val next = children
                .filter { it.type == type && it.index != null }
                .sortedBy { it.index }
                .firstOrNull { (it.index ?: -1) == currentIndex + 1 }
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
                .sortedBy { it.index }
                .firstOrNull()
        } catch (_: Exception) {
            null
        }
    }
}
