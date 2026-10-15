package tv.onscreen.mobile.ui.book

import tv.onscreen.mobile.data.model.ReadingPosition
import tv.onscreen.mobile.data.model.ReadingProgress
import tv.onscreen.mobile.ui.item.parseRfc3339Millis
import java.text.SimpleDateFormat
import java.util.Locale
import java.util.TimeZone

/**
 * Reading-position sync for the page reader (v2.6): the position lives on the
 * server (GET/PUT /items/{id}/reading-progress) so another device opens where
 * this one left off, with a copy on this device for a save that didn't reach
 * the server. Same rules and locator format as the web reader
 * (web/src/routes/books/[id]/reading-sync.ts). Pure, so it is unit-tested on
 * the JVM (ReadingSyncTest).
 */
object ReadingSync {

    /** How long the reader waits after a page turn before saving to the
     *  server — a flick through ten pages is one save, not ten. */
    const val SAVE_DEBOUNCE_MS = 2_000L

    /** Page-based books store the 1-indexed page number as the locator. */
    fun pageLocator(page: Int): String = page.toString()

    /** The page a locator names, or null when it isn't a page number (an
     *  EPUB CFI saved by another reader). */
    fun pageFromLocator(locator: String): Int? {
        if (locator.isEmpty() || !locator.all { it.isDigit() }) return null
        return locator.toIntOrNull()?.takeIf { it >= 1 }
    }

    /** Progression for a page-based book: the last page reads as 1 (the
     *  server marks the book read at 0.98). */
    fun pageProgression(page: Int, pageCount: Int): Double =
        if (pageCount <= 0) 0.0 else (page.toDouble() / pageCount).coerceIn(0.0, 1.0)

    /** The server's position, with its time. */
    fun fromServer(p: ReadingProgress?): ReadingPosition? =
        p?.takeIf { it.locator.isNotBlank() }?.let {
            ReadingPosition(it.locator, it.progression, parseRfc3339Millis(it.updated_at) ?: 0L)
        }

    /** The newer of the two (last write wins, as on the server); either may
     *  be missing. A tie keeps this device's. */
    fun newer(local: ReadingPosition?, server: ReadingPosition?): ReadingPosition? = when {
        local == null -> server
        server == null -> local
        server.updatedAtMs > local.updatedAtMs -> server
        else -> local
    }

    /** The page to open a book of [pageCount] pages at: [position]'s, clamped,
     *  else the first. */
    fun startPage(position: ReadingPosition?, pageCount: Int): Int {
        val page = position?.let { pageFromLocator(it.locator) } ?: return 1
        return page.coerceIn(1, pageCount.coerceAtLeast(1))
    }

    /** RFC 3339 in UTC, as the server parses time.Time. */
    fun formatTime(ms: Long): String =
        SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.ROOT)
            .apply { timeZone = TimeZone.getTimeZone("UTC") }
            .format(java.util.Date(ms))
}
