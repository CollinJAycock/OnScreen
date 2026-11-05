package tv.onscreen.mobile.ui.library

import tv.onscreen.mobile.data.model.MediaItem

/**
 * A library listing as a sparse array: [total] rows (the server's count),
 * filled one [pageSize] page at a time wherever the grid is looking. A row
 * whose page hasn't loaded reads as null and the grid draws a placeholder
 * card in its place.
 *
 * Why sparse rather than an appended list: the A-Z rail jumps straight to a
 * letter's row (`/items/letters` gives its offset), and a library can hold
 * 10k+ items. With an appended list a jump to "W" had to page in everything
 * before it; here the grid is [total] long from the first page, so a jump is
 * a scrollToItem plus the one or two pages around it, and scrolling back up
 * from there fills the earlier pages the same way.
 *
 * Pages are aligned to [pageSize] (page n = rows n*pageSize until
 * (n+1)*pageSize, fetched as `limit=pageSize&offset=n*pageSize`). Immutable:
 * every change returns a new instance for the UI state.
 */
class LibraryPages(
    val total: Int = 0,
    private val pages: Map<Int, List<MediaItem>> = emptyMap(),
    val pageSize: Int = DEFAULT_PAGE_SIZE,
) {
    init {
        require(pageSize > 0) { "pageSize must be positive" }
    }

    /** The item at listing row [index], or null while its page isn't loaded
     *  (or past [total]). */
    operator fun get(index: Int): MediaItem? {
        if (index < 0 || index >= total) return null
        return pages[index / pageSize]?.getOrNull(index % pageSize)
    }

    val loadedPages: Set<Int> get() = pages.keys

    fun isLoaded(page: Int): Boolean = page in pages

    /** Every loaded item, in listing order (gaps skipped). */
    val loadedItems: List<MediaItem>
        get() = pages.keys.sorted().flatMap { p ->
            val rows = pages.getValue(p)
            // Rows past the end of a shrunken listing aren't part of it.
            val room = (total - p * pageSize).coerceIn(0, rows.size)
            rows.take(room)
        }

    /** The pages that hold rows [first]..[last], clamped to the listing. */
    fun pagesFor(first: Int, last: Int): IntRange {
        if (total == 0 || last < first) return IntRange.EMPTY
        val lo = first.coerceIn(0, total - 1) / pageSize
        val hi = last.coerceIn(0, total - 1) / pageSize
        return lo..hi
    }

    /**
     * [page] landed with [items] and the server's [total] at that moment.
     * The newest total wins (items added or removed meanwhile grow or
     * shrink the grid). A short page ends the listing where it stops: past
     * it there is nothing to load, so the grid mustn't keep placeholders
     * (and refetch) there — the same guard the appended list had against
     * an inconsistent total.
     */
    fun withPage(page: Int, items: List<MediaItem>, total: Int): LibraryPages {
        val start = page * pageSize
        val newTotal = if (items.size < pageSize) minOf(total, start + items.size) else total
        return LibraryPages(maxOf(newTotal, 0), pages + (page to items), pageSize)
    }

    /** The same rows with [transform] applied to every loaded item. */
    fun mapItems(transform: (MediaItem) -> MediaItem): LibraryPages =
        LibraryPages(total, pages.mapValues { (_, rows) -> rows.map(transform) }, pageSize)

    /** Only the loaded pages [keep] accepts; the rest read as unloaded. */
    fun retainPages(keep: (Int) -> Boolean): LibraryPages =
        LibraryPages(total, pages.filterKeys(keep), pageSize)

    /** This listing plus any page [other] has that this one doesn't (a page
     *  the grid loaded while this one was being re-fetched). */
    fun plusMissingFrom(other: LibraryPages): LibraryPages {
        if (other.pageSize != pageSize) return this
        val extra = other.pages.filterKeys { it !in pages }
        return if (extra.isEmpty()) this else LibraryPages(total, pages + extra, pageSize)
    }

    /** The loaded copy of the item with [id], if any. */
    fun find(id: String): MediaItem? {
        for (rows in pages.values) rows.firstOrNull { it.id == id }?.let { return it }
        return null
    }

    companion object {
        const val DEFAULT_PAGE_SIZE = 100
    }
}
