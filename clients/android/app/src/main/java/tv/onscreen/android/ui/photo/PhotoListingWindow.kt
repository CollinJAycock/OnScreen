package tv.onscreen.android.ui.photo

import kotlinx.coroutines.CancellationException
import kotlin.math.abs

/**
 * The photos of one library listing (its sort and filters), read a page at a
 * time around the one on screen — what the photo viewer steps through when it
 * is opened from a library grid, or from elsewhere once the server has said
 * where the photo sits ([located]).
 *
 * The grid used to hand the viewer every photo id it had loaded, in the
 * fragment's arguments: after a long scroll that is thousands of ids in a
 * Bundle the system keeps for the back stack, and HOME then risked a
 * TransactionTooLargeException (~10k ids). Now the viewer gets the library,
 * the query and the photo's row in the listing, and holds at most [maxPages]
 * pages of it.
 *
 * [fetch] reads [limit] rows from [offset] and the listing's total. Main
 * thread (the viewer's), so no locking.
 */
internal class PhotoListingWindow(
    startIndex: Int,
    private val pageSize: Int = PAGE_SIZE,
    private val maxPages: Int = MAX_PAGES,
    private val fetch: suspend (offset: Int, limit: Int) -> Pair<List<Entry>, Int>,
) {
    /** One listing row: its item id, and whether it is a photo (the viewer
     *  steps over anything else). */
    data class Entry(val id: String, val isPhoto: Boolean)

    /** The listing row on screen. */
    var index: Int = startIndex.coerceAtLeast(0)
        private set

    /** Rows in the listing; 0 until a page has loaded. */
    var total: Int = 0
        private set

    /** Loaded pages by their first row. */
    private val pages = mutableMapOf<Int, List<Entry>>()

    /** Load the page around the start row and settle on [itemId]: at the
     *  start row, or wherever that page has it (the listing can shift
     *  between the grid and here). False when it isn't found or nothing
     *  loads — the caller falls back to resolving siblings itself. */
    suspend fun start(itemId: String): Boolean {
        if (entryAt(index)?.id == itemId) return true
        val pageStart = pageOf(index)
        val at = pages[pageStart]?.indexOfFirst { it.id == itemId } ?: -1
        if (at < 0) return false
        index = pageStart + at
        return true
    }

    /**
     * Move [delta] (+1 / -1) to the next photo in the listing, wrapping at its
     * ends, and return its id. Null when nothing could be loaded, or no photo
     * turned up within [maxPages] pages' worth of rows.
     */
    suspend fun step(delta: Int): String? {
        if (total <= 0) return null
        var i = index
        repeat(minOf(total, pageSize * maxPages)) {
            i = ((i + delta) % total + total) % total
            val e = entryAt(i) ?: return null
            if (e.isPhoto) {
                index = i
                return e.id
            }
        }
        return null
    }

    /** Loaded pages, for the tests. */
    internal fun loadedPages(): Set<Int> = pages.keys.toSet()

    private fun pageOf(row: Int): Int = (row / pageSize) * pageSize

    private suspend fun entryAt(row: Int): Entry? {
        val pageStart = pageOf(row)
        val page = pages[pageStart] ?: load(pageStart) ?: return null
        return page.getOrNull(row - pageStart)
    }

    private suspend fun load(pageStart: Int): List<Entry>? {
        val (rows, count) = try {
            fetch(pageStart, pageSize)
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            return null
        }
        total = count
        pages[pageStart] = rows
        // Keep the pages nearest this one.
        while (pages.size > maxPages) {
            pages.remove(pages.keys.maxBy { abs(it - pageStart) })
        }
        return rows
    }

    companion object {
        const val PAGE_SIZE = 50
        const val MAX_PAGES = 3

        /**
         * The window for a photo opened without its row (Home, Search, a hub
         * row): [locate] asks the server for the row (GET
         * /libraries/{id}/items/position, same query as [fetch]) and the
         * window opens there. Null when that fails or is a 404 (the photo
         * isn't in that listing, or the server predates the endpoint), or
         * the page there doesn't hold the photo — the viewer then resolves
         * siblings as it did before.
         */
        suspend fun located(
            itemId: String,
            locate: suspend () -> Int?,
            pageSize: Int = PAGE_SIZE,
            maxPages: Int = MAX_PAGES,
            fetch: suspend (offset: Int, limit: Int) -> Pair<List<Entry>, Int>,
        ): PhotoListingWindow? {
            val row = try {
                locate()
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                null
            } ?: return null
            val window = PhotoListingWindow(row, pageSize, maxPages, fetch)
            return if (window.start(itemId)) window else null
        }
    }
}
