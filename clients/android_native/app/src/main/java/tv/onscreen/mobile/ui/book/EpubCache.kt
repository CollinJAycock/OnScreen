package tv.onscreen.mobile.ui.book

import android.content.Context
import java.io.File

/**
 * Where BookReaderViewModel keeps fetched EPUBs (`cacheDir/book-epubs`), and
 * the cap on it. Whole books were kept forever, so the directory only grew;
 * after each fetch it is trimmed back to [MAX_BYTES], least recently opened
 * first. It is the signed-in account's library, so a sign-out empties it
 * (IdentityCaches).
 */
internal object EpubCache {
    const val DIR = "book-epubs"

    /** Total size kept. A typical EPUB is 1-20 MB. */
    const val MAX_BYTES = 200L * 1024 * 1024

    fun dir(context: Context): File = File(context.cacheDir, DIR)

    /** Delete the least recently used books (by mtime; a cache hit touches
     *  its file) until the rest fit in [maxBytes]. [keep] is never deleted:
     *  the book being opened, even when it alone is over the cap. A `.tmp`
     *  (a fetch still in progress) is left alone. */
    fun trim(dir: File, keep: File, maxBytes: Long = MAX_BYTES) {
        val files = dir.listFiles()?.filter { it.isFile && !it.name.endsWith(".tmp") }.orEmpty()
        var total = files.sumOf { it.length() }
        if (total <= maxBytes) return
        for (f in files.sortedBy { it.lastModified() }) {
            if (total <= maxBytes) break
            if (f.canonicalPath == keep.canonicalPath) continue
            val size = f.length()
            if (f.delete()) total -= size
        }
    }
}
