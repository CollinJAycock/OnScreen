package tv.onscreen.android.ui.common

import androidx.leanback.widget.ArrayObjectAdapter

/**
 * Update an [ArrayObjectAdapter] in place while PRESERVING the grid's focus and
 * scroll position.
 *
 * The previous pattern across the browse/grid screens — `clear()` then re-add
 * every item on each state emission — removed the currently focused view, so
 * Leanback's VerticalGrid/Browse snapped selection back to the top on every
 * reload (the `onResume` refresh) AND on every pagination page-in. That read to
 * users as the UI "jumping to the top" or focus disappearing mid-scroll.
 *
 * Behaviour, comparing [newItems] to the current contents by [id]:
 *  - identical (same ids, same order) -> no-op, keep focus/scroll
 *  - pure append (the current ids are a prefix of the new list) -> add only the
 *    new tail, so pagination streams in without disturbing the user's position
 *  - otherwise (reorder / removal / changed content) -> rebuild
 */
fun <T : Any> ArrayObjectAdapter.syncItems(newItems: List<T>, id: (T) -> Any?) {
    val current = ArrayList<Any?>(size())
    for (i in 0 until size()) {
        @Suppress("UNCHECKED_CAST")
        current.add(id(get(i) as T))
    }
    val incoming = newItems.map(id)
    if (current == incoming) return
    // Pure append (e.g. the next pagination page): keep existing rows + focus.
    if (incoming.size > current.size && incoming.subList(0, current.size) == current) {
        for (i in current.size until newItems.size) add(newItems[i])
        return
    }
    // Reset / reorder / removal: rebuild from scratch.
    clear()
    for (item in newItems) add(item)
}
