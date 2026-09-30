package tv.onscreen.mobile.ui.item

import tv.onscreen.mobile.data.model.ChildItem

/*
 * An album's track list, disc by disc. Every disc restarts at track 1, so an
 * album that spans more than one disc heads each disc's run of tracks
 * ("Disc 2") for its numbering to read right. A track without a disc number
 * is on disc 1. Mirrors the web's albumDiscs.ts.
 */

/** One disc's tracks, in play order. */
data class DiscGroup(val disc: Int, val tracks: List<ChildItem>)

/**
 * [children] in [ChildItem.PLAY_ORDER] (disc, then track number, unnumbered
 * tracks last on their disc), split into one group per disc. Returns new
 * lists; the page's own listing keeps the server's order.
 */
fun albumDiscGroups(children: List<ChildItem>): List<DiscGroup> {
    val groups = mutableListOf<DiscGroup>()
    var disc = 0
    var run = mutableListOf<ChildItem>()
    for (track in children.sortedWith(ChildItem.PLAY_ORDER)) {
        val d = track.disc_number ?: 1
        if (run.isNotEmpty() && d != disc) {
            groups += DiscGroup(disc, run)
            run = mutableListOf()
        }
        disc = d
        run += track
    }
    if (run.isNotEmpty()) groups += DiscGroup(disc, run)
    return groups
}
