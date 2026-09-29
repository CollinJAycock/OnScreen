package tv.onscreen.android.playback

import java.math.BigDecimal
import java.math.RoundingMode

/**
 * Listening speed for audiobooks: the presets the speed picker offers, the
 * range the server accepts, the label, and which items get a speed at all.
 * Pure (no Android types) so it is JVM-tested — see AudiobookSpeedTest.
 * Mirrors the phone client's AudiobookSpeed.
 *
 * Speed is per BOOK: a chapter plays at its book's speed. Everything that
 * isn't an audiobook plays at 1× — music especially. Media3's
 * PlaybackParameters(speed) keeps the pitch.
 */
object AudiobookSpeed {

    /** The server's accepted range (audiobook.go Min/MaxPlaybackRate). */
    const val MIN = 0.5f
    const val MAX = 3.0f
    const val NORMAL = 1.0f

    /** What the speed picker lists, slowest first. */
    val PRESETS: List<Float> = listOf(0.75f, 1.0f, 1.25f, 1.5f, 1.75f, 2.0f, 2.5f, 3.0f)

    /** A rate brought into [MIN]..[MAX] and to two decimals, as the server
     *  stores it. Anything unusable (NaN, infinite) is [NORMAL]. */
    fun clamp(rate: Double): Float {
        if (rate.isNaN() || rate.isInfinite()) return NORMAL
        val bounded = rate.coerceIn(MIN.toDouble(), MAX.toDouble())
        return BigDecimal(bounded).setScale(2, RoundingMode.HALF_UP).toFloat()
    }

    fun clamp(rate: Float): Float = clamp(rate.toDouble())

    /** "1×", "1.25×", "0.75×" — no trailing zeros, always a '.' decimal. */
    fun label(rate: Float): String {
        val plain = BigDecimal(clamp(rate).toDouble())
            .setScale(2, RoundingMode.HALF_UP)
            .stripTrailingZeros()
            .toPlainString()
        return "$plain×"
    }

    /** Whether [a] and [b] are the same speed, to the server's precision. */
    fun same(a: Float, b: Float): Boolean = kotlin.math.abs(a - b) < 0.005f

    /** Index of the preset [rate] is at, or -1 when it's between presets
     *  (a speed set on another client). */
    fun presetIndex(rate: Float): Int = PRESETS.indexOfFirst { same(it, rate) }

    /** Item types that play at a book's speed. */
    fun hasSpeed(type: String?): Boolean = type == AUDIOBOOK || type == CHAPTER

    /**
     * The book whose speed [itemId] plays at: itself for an audiobook, its
     * parent for a chapter. Null for everything else (it plays at 1×) and
     * for a chapter with no parent.
     */
    fun bookIdOf(type: String?, itemId: String, parentId: String?): String? = when (type) {
        AUDIOBOOK -> itemId
        CHAPTER -> parentId?.takeIf { it.isNotEmpty() }
        else -> null
    }

    /**
     * Whether a player moving on from an item of [fromBookId] to one of
     * [toBookId] keeps the speed it's at: only within one book (chapter to
     * chapter). The player's speed IS that book's — including a change made
     * mid-book that hasn't reached the server — so looking it up again could
     * only make it jump. Another book, or anything that isn't one, re-decides.
     */
    fun keepsSpeed(fromBookId: String?, toBookId: String?): Boolean =
        toBookId != null && toBookId == fromBookId

    /**
     * The speed a chain from a [fromType] item of [fromBookId] to a [toType]
     * item hands on: the player's [rate], as that book's, from one chapter to
     * another — the book's next chapter, so the same book. Null for anything
     * else: the next item's speed is its own (another book's, or 1×).
     */
    fun carried(fromType: String?, fromBookId: String?, toType: String?, rate: Float): BookSpeed? =
        if (fromType == CHAPTER && toType == CHAPTER && !fromBookId.isNullOrEmpty()) {
            BookSpeed(fromBookId, clamp(rate))
        } else {
            null
        }

    const val AUDIOBOOK = "audiobook"
    const val CHAPTER = "audiobook_chapter"
}

/** A book's speed handed from one chapter's player to the next chapter's
 *  (PlaybackFragment's chain builds a fresh player, which starts at 1×). */
data class BookSpeed(val bookId: String, val rate: Float)
