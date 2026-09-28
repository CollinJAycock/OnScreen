package tv.onscreen.mobile.playback

import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.repository.ItemRepository

/** Stateless lookup helper that finds the next item to play after a
 *  given track or episode. Same logic as the TV client's resolver —
 *  in-container next sibling first (S04E12 → S04E13, album track 5
 *  → track 6), then cross-container fall-through (last episode of
 *  S04 → S05E01, last track of album A → first track of album B). */
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
            val nextContainer = nextContainer(parentId, type) ?: return null
            itemRepo.getChildren(nextContainer.id)
                .filter { it.type == type && it.index != null }
                .sortedBy { it.index }
                .firstOrNull()
        } catch (_: Exception) {
            null
        }
    }

    /** The container after [containerId] under the same grandparent — the
     *  artist's next album (by year, then index) for a track, the show's next
     *  season for an episode. Null for other types, at the end, or on any
     *  lookup failure. Also used by PlaybackService to append the next album
     *  to a music queue before the current one runs out (gapless across
     *  albums). */
    suspend fun nextContainer(containerId: String, leafType: String): ChildItem? {
        if (leafType != "track" && leafType != "episode") return null
        return try {
            val parent = itemRepo.getItem(containerId)
            val grandparentId = parent.parent_id ?: return null
            val containerType = if (leafType == "track") "album" else "season"
            val rawSiblings = itemRepo.getChildren(grandparentId)
                .filter { it.type == containerType }
            val siblings = if (leafType == "track") {
                rawSiblings.sortedWith(MusicQueue.ALBUM_ORDER)
            } else {
                rawSiblings.sortedBy { it.index ?: Int.MAX_VALUE }
            }
            val currentIdx = siblings.indexOfFirst { it.id == containerId }
            if (currentIdx < 0) return null
            siblings.getOrNull(currentIdx + 1)
        } catch (_: Exception) {
            null
        }
    }
}
