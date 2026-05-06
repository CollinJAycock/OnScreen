package tv.onscreen.mobile.lyrics

/**
 * LRC = Lyric file format. Each line is one of:
 *   - `[mm:ss.xx]Lyric text` — a timed cue
 *   - `[mm:ss.xx][mm:ss.xx]Lyric text` — same line repeats at multiple
 *     timestamps (rare but valid; we expand into multiple cues)
 *   - `[ar:Artist]`, `[ti:Title]`, `[al:Album]`, `[length:mm:ss]` —
 *     metadata; we ignore these
 *   - empty / whitespace — ignored
 *
 * Pure module — no Android imports — so the parser is JVM-testable
 * without spinning up a player.
 */
object LrcParser {

    /** One timed lyric line. text="" represents an instrumental
     *  interval (LRC writers sometimes mark the intro / interlude
     *  this way). UI renders empty-text cues as a faded `♪` glyph. */
    data class Cue(val timeMs: Long, val text: String)

    /** Regex matching one `[mm:ss.xx]` or `[mm:ss]` timestamp. The
     *  fractional digits are optional and may be 1-3 chars; we
     *  normalise to ms in [parseTimestamp]. */
    private val TIMESTAMP = Regex("""\[(\d{1,2}):(\d{1,2})(?:\.(\d{1,3}))?]""")

    /** Parse the full LRC body. Returns cues in ascending-time order
     *  (LRC writers don't always emit them sorted; binary-search
     *  lookups assume sorted). */
    fun parse(body: String): List<Cue> {
        val out = mutableListOf<Cue>()
        body.lineSequence().forEach { rawLine ->
            val line = rawLine.trim()
            if (line.isEmpty()) return@forEach
            // Pull every timestamp at the start of the line. Stop at
            // the first character that isn't a `[mm:ss.xx]` block —
            // that's the lyric text.
            val matches = TIMESTAMP.findAll(line).toList()
            if (matches.isEmpty()) return@forEach
            // Lyric text starts after the last timestamp's range-end.
            val textStart = matches.last().range.last + 1
            val text = if (textStart >= line.length) "" else line.substring(textStart).trim()
            for (m in matches) {
                val mm = m.groupValues[1].toIntOrNull() ?: continue
                val ss = m.groupValues[2].toIntOrNull() ?: continue
                val frac = m.groupValues[3]
                val ms = (mm * 60L + ss) * 1000L + parseFrac(frac)
                out.add(Cue(ms, text))
            }
        }
        out.sortBy { it.timeMs }
        return out
    }

    /** Parse the optional fractional-seconds suffix to ms. ".5" → 500,
     *  ".50" → 500, ".500" → 500. Anything malformed → 0. */
    private fun parseFrac(s: String): Long {
        if (s.isEmpty()) return 0L
        val padded = s.padEnd(3, '0').take(3)
        return padded.toLongOrNull() ?: 0L
    }

    /**
     * Look up the cue active at [positionMs]. Returns the cue whose
     * timeMs is the largest value ≤ positionMs — i.e. "the line that
     * started before now and hasn't been replaced by a later one yet".
     *
     * Returns null when [positionMs] precedes the first cue (intro
     * before the first sung line) or [cues] is empty.
     */
    fun cueAt(cues: List<Cue>, positionMs: Long): Cue? {
        if (cues.isEmpty()) return null
        if (positionMs < cues.first().timeMs) return null
        // Binary search for largest index where timeMs <= positionMs.
        var lo = 0
        var hi = cues.size - 1
        var best = -1
        while (lo <= hi) {
            val mid = (lo + hi) ushr 1
            if (cues[mid].timeMs <= positionMs) {
                best = mid
                lo = mid + 1
            } else {
                hi = mid - 1
            }
        }
        return if (best >= 0) cues[best] else null
    }
}
