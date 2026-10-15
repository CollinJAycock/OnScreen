package tv.onscreen.android.data.model

/**
 * Multi-episode files (v2.6): an "S01E01-E02" file is one item with index 1
 * and index_end 2. The server omits index_end for a single episode; a value
 * that doesn't run past index (a bad tag) reads as a single episode too.
 */
object EpisodeRange {

    /** [indexEnd] when the item covers a range starting at [index], else null. */
    fun end(index: Int?, indexEnd: Int?): Int? =
        indexEnd?.takeIf { index != null && it > index }

    /** The last number the item covers: the range's end, else [index]. The
     *  next episode is the first one after this. */
    fun last(index: Int?, indexEnd: Int?): Int? = end(index, indexEnd) ?: index
}
