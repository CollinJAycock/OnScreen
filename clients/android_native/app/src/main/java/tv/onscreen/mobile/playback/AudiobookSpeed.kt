package tv.onscreen.mobile.playback

import java.math.BigDecimal
import java.math.RoundingMode

/**
 * Listening speed for audiobooks: the presets the player offers, the range
 * the server accepts, the label, and which items get a speed at all. Pure
 * (no Android types) so it is JVM-tested — see AudiobookSpeedTest.
 *
 * Speed is per BOOK: a chapter plays at its book's speed, so chaining from
 * chapter to chapter keeps it. Everything that isn't an audiobook plays at
 * 1× — music especially, where a book's 1.5× would be plainly wrong.
 * Media3's PlaybackParameters(speed) keeps the pitch, so a faster book
 * doesn't sound chipmunked.
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

    /** Whether [a] and [b] are the same speed, to the server's precision.
     *  The picker marks the preset the player is at with this. */
    fun same(a: Float, b: Float): Boolean = kotlin.math.abs(a - b) < 0.005f

    /** Item types that play at a book's speed. */
    fun hasSpeed(type: String?): Boolean = type == AUDIOBOOK || type == CHAPTER

    /**
     * The book whose speed [itemId] plays at: itself for an audiobook, its
     * parent for a chapter. Null for everything else (it plays at 1×) and
     * for a chapter with no parent, which the server can't resolve either.
     */
    fun bookIdOf(type: String?, itemId: String, parentId: String?): String? = when (type) {
        AUDIOBOOK -> itemId
        CHAPTER -> parentId?.takeIf { it.isNotEmpty() }
        else -> null
    }

    const val AUDIOBOOK = "audiobook"
    const val CHAPTER = "audiobook_chapter"
}
