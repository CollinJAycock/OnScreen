package tv.onscreen.mobile.ui.watchlist

import tv.onscreen.mobile.data.model.WatchStatus
import tv.onscreen.mobile.data.model.WatchlistCounts
import tv.onscreen.mobile.data.model.WatchlistItem
import tv.onscreen.mobile.data.model.WatchlistStatusCount

/*
 * Pure helpers behind the Watchlist screen: tab + type-filter labels, card
 * subtitles and the local list update after a status change. No Compose, no
 * network — unit-tested in WatchlistLabelsTest. Ported from the web client's
 * $lib/watchlist.ts so the labels read the same on every client.
 */

/** Tab order: Plan to Watch first, then the tracker progression (the enum's
 *  declaration order). */
val WATCHLIST_STATUSES: List<WatchStatus> = WatchStatus.entries

/** Tab / menu label for a status. */
fun statusLabel(s: WatchStatus): String = when (s) {
    WatchStatus.PLAN_TO_WATCH -> "Plan to Watch"
    WatchStatus.WATCHING -> "Watching"
    WatchStatus.ON_HOLD -> "On Hold"
    WatchStatus.COMPLETED -> "Completed"
    WatchStatus.DROPPED -> "Dropped"
}

private val TYPE_LABELS = mapOf(
    "movie" to "Movies",
    "show" to "Shows",
    "season" to "Seasons",
    "episode" to "Episodes",
    "track" to "Tracks",
    "album" to "Albums",
    "artist" to "Artists",
    "photo" to "Photos",
    "music_video" to "Music videos",
    "audiobook" to "Audiobooks",
    "audiobook_chapter" to "Audiobook chapters",
    "book_author" to "Authors",
    "book_series" to "Book series",
    "podcast" to "Podcasts",
    "podcast_episode" to "Podcast episodes",
    "home_video" to "Home videos",
    "book" to "Books",
)

/** Plural label for a media type in the type filter ("Movies"); an unknown
 *  type reads as its wire value with spaces. */
fun typeLabel(type: String): String = TYPE_LABELS[type] ?: type.replace('_', ' ')

data class TypeOption(val type: String, val label: String, val count: Int)

/** The type filter's options for one tab: only types the caller has under
 *  that status, most common first (ties by label). Empty when counts are
 *  unknown — the screen then shows no filter. */
fun typeOptions(counts: WatchlistCounts?, status: WatchStatus): List<TypeOption> =
    counts?.statuses?.get(status.wire)?.types.orEmpty()
        .filter { it.value > 0 }
        .map { (type, n) -> TypeOption(type, typeLabel(type), n) }
        .sortedWith(compareByDescending<TypeOption> { it.count }.thenBy { it.label })

/** Badge number for a status tab: null when counts are unknown, 0 for a
 *  status the server left out (it has nothing in it). */
fun tabCount(counts: WatchlistCounts?, status: WatchStatus): Int? =
    counts?.let { it.statuses[status.wire]?.total ?: 0 }

/** "Plan to Watch (3)", or the bare label while counts are unknown. */
fun tabLabel(counts: WatchlistCounts?, status: WatchStatus): String {
    val n = tabCount(counts, status)
    return if (n == null) statusLabel(status) else "${statusLabel(status)} ($n)"
}

/** "S1 E2"; a missing half is dropped. The server sends 0 for an unrecorded
 *  episode number, so E0 is dropped too (season 0 is Specials, kept). */
private fun seasonEpisode(season: Int?, episode: Int?): String =
    listOfNotNull(
        season?.let { "S$it" },
        episode?.takeIf { it > 0 }?.let { "E$it" },
    ).joinToString(" ")

/**
 * The line under a card's title: "Show · S1 E2" for an episode, the show for
 * a season, "Artist · Album" for a track, the parent (author / artist) and
 * year for an album / book, else the year. Empty = no second line.
 */
fun cardSubtitle(item: WatchlistItem): String = when (item.type) {
    "episode" -> listOf(item.grandparent_title.orEmpty(), seasonEpisode(item.parent_index, item.index))
        .filter { it.isNotBlank() }
        .joinToString(" · ")
    "season" -> item.parent_title.orEmpty()
    "track" -> listOfNotNull(item.grandparent_title, item.parent_title)
        .filter { it.isNotBlank() }
        .joinToString(" · ")
    "album", "audiobook", "book" -> listOfNotNull(item.parent_title, item.year?.toString())
        .filter { it.isNotBlank() }
        .joinToString(" · ")
    else -> item.year?.toString().orEmpty()
}

/** Statuses a card's "Move to…" menu offers: every one but its current. */
fun moveTargets(item: WatchlistItem): List<WatchStatus> =
    WATCHLIST_STATUSES.filter { it.wire != item.status }

data class StatusChange(
    val items: List<WatchlistItem>,
    val counts: WatchlistCounts?,
    /** The card left the loaded page (the caller drops its total by one). */
    val removed: Boolean,
)

/**
 * Apply a status change made from a card to the loaded page and counts: the
 * item leaves the current tab (moved or removed) and the tab badges shift
 * with it. [next] null = removed from the watchlist; [next] equal to the
 * item's current status changes nothing.
 */
fun applyStatusChange(
    items: List<WatchlistItem>,
    counts: WatchlistCounts?,
    item: WatchlistItem,
    next: WatchStatus?,
): StatusChange {
    if (next != null && next.wire == item.status) return StatusChange(items, counts, removed = false)
    val remaining = items.filter { it.id != item.id }
    val removed = remaining.size != items.size
    if (counts == null) return StatusChange(remaining, null, removed)

    val statuses = counts.statuses.toMutableMap()
    fun bump(status: String, delta: Int) {
        val cur = statuses[status] ?: WatchlistStatusCount()
        val types = cur.types.toMutableMap()
        val n = maxOf(0, (types[item.type] ?: 0) + delta)
        if (n == 0) types.remove(item.type) else types[item.type] = n
        statuses[status] = WatchlistStatusCount(total = maxOf(0, cur.total + delta), types = types)
    }
    bump(item.status, -1)
    if (next != null) bump(next.wire, 1)
    return StatusChange(
        items = remaining,
        counts = WatchlistCounts(
            total = maxOf(0, counts.total - if (next == null) 1 else 0),
            statuses = statuses,
        ),
        removed = removed,
    )
}
