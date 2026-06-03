package tv.onscreen.android.ui.common

/**
 * Remembers a VerticalGrid's selected position across a fragment VIEW recreation.
 *
 * Opening a detail screen uses replace()+addToBackStack: the grid fragment's view
 * is destroyed (so the next onViewCreated builds a fresh, empty adapter that
 * repopulates from position 0 — i.e. the grid snaps back to the top), while the
 * fragment INSTANCE stays on the back stack. Holding the position on the instance
 * (this object) survives that round-trip; we re-apply it once the recreated grid
 * has repopulated.
 *
 * Usage in a VerticalGridSupportFragment:
 *   private val scroll = GridScrollMemory()
 *   // after `adapter = gridAdapter`:        scroll.onViewRecreated()
 *   // in the items collector, post-populate: scroll.restoreIfPending(gridAdapter.size(), ::setSelectedPosition)
 *   // in setOnItemViewSelectedListener:      scroll.record(gridAdapter.indexOf(item))
 */
class GridScrollMemory {
    private var savedPosition = 0
    private var restorePending = false

    /** Call once the (possibly recreated) view+adapter are set up. Arms a restore
     *  only when we actually have a remembered position (i.e. a return, not a
     *  first open). */
    fun onViewRecreated() {
        restorePending = savedPosition > 0
    }

    /** Record the current selection so a later view recreation can return to it. */
    fun record(position: Int) {
        if (position >= 0) savedPosition = position
    }

    /** After the adapter repopulates, re-apply the remembered position exactly once
     *  (only if it's within the loaded range — a deep position past the first page
     *  waits harmlessly). */
    fun restoreIfPending(adapterSize: Int, apply: (Int) -> Unit) {
        if (restorePending && savedPosition in 1 until adapterSize) {
            restorePending = false
            apply(savedPosition)
        }
    }
}
