package tv.onscreen.mobile.trickplay

import tv.onscreen.mobile.data.model.TrickplayCue

/**
 * Parser for the trickplay WebVTT payload OnScreen serves at
 * `/api/v1/items/{id}/trickplay/index.vtt`. Each cue is shaped like:
 *
 * ```
 * 00:00:00.000 --> 00:00:10.000
 * sprite_000.jpg#xywh=0,0,320,180
 * ```
 *
 * The body line carries the sprite filename plus an `xywh` fragment
 * naming the rectangle to crop out of the sprite sheet for that
 * 10-second window.
 *
 * Pure module — no Android imports — so this can run in JVM unit tests
 * (and theoretically in a worker thread off-main).
 */
object TrickplayVtt {

    /** Parse the full VTT body. Skips malformed cues rather than
     *  failing the whole document — a stray "WEBVTT" comment line or
     *  a NOTE block won't reject the rest. Returns cues in document
     *  order; callers can binary-search by start time. */
    fun parse(body: String): List<TrickplayCue> {
        val out = mutableListOf<TrickplayCue>()
        // Split into stanzas on blank lines. WebVTT is line-oriented;
        // each cue is a contiguous block separated by an empty line.
        val stanzas = body.split(Regex("\r?\n\r?\n"))
        for (stanza in stanzas) {
            val cue = parseCue(stanza) ?: continue
            out.add(cue)
        }
        return out
    }

    /** Parse one VTT stanza (without the trailing blank line). Returns
     *  null when it isn't a real cue (header, NOTE, malformed). */
    private fun parseCue(stanza: String): TrickplayCue? {
        val lines = stanza.split(Regex("\r?\n")).filter { it.isNotBlank() }
        if (lines.isEmpty()) return null
        // Find the timing line (contains " --> "). VTT allows an
        // optional cue-identifier line above it; we just scan.
        val timingIdx = lines.indexOfFirst { it.contains(" --> ") }
        if (timingIdx < 0) return null
        if (timingIdx + 1 >= lines.size) return null
        val timing = lines[timingIdx]
        val payload = lines[timingIdx + 1]

        val arrow = timing.indexOf(" --> ")
        val startStr = timing.substring(0, arrow).trim()
        // End time may be followed by cue-settings (line, position
        // etc.) separated by spaces — strip those.
        val endStr = timing.substring(arrow + 5).trim().substringBefore(' ').trim()
        val startMs = parseTime(startStr) ?: return null
        val endMs = parseTime(endStr) ?: return null

        // Payload: `sprite_000.jpg#xywh=0,0,320,180`. The hash anchor
        // is the optional fragment from the Media Fragments URI spec.
        val hashIdx = payload.indexOf('#')
        if (hashIdx < 0) return null
        val file = payload.substring(0, hashIdx).trim()
        val frag = payload.substring(hashIdx + 1).trim()
        if (!frag.startsWith("xywh=")) return null
        val nums = frag.substring(5).split(',')
        if (nums.size != 4) return null
        val x = nums[0].trim().toIntOrNull() ?: return null
        val y = nums[1].trim().toIntOrNull() ?: return null
        val w = nums[2].trim().toIntOrNull() ?: return null
        val h = nums[3].trim().toIntOrNull() ?: return null

        return TrickplayCue(startMs = startMs, endMs = endMs, file = file, x = x, y = y, w = w, h = h)
    }

    /** Parse a VTT timestamp like `00:01:23.456` or `01:23.456` to ms.
     *  Returns null on any malformed input — the caller drops the cue. */
    internal fun parseTime(s: String): Long? {
        val parts = s.split(':')
        // VTT permits H:MM:SS.mmm or MM:SS.mmm. Anything else is junk.
        val h: Int
        val m: Int
        val secStr: String
        when (parts.size) {
            3 -> {
                h = parts[0].toIntOrNull() ?: return null
                m = parts[1].toIntOrNull() ?: return null
                secStr = parts[2]
            }
            2 -> {
                h = 0
                m = parts[0].toIntOrNull() ?: return null
                secStr = parts[1]
            }
            else -> return null
        }
        val dotIdx = secStr.indexOf('.')
        val whole: Int
        val frac: Int
        if (dotIdx >= 0) {
            whole = secStr.substring(0, dotIdx).toIntOrNull() ?: return null
            // Pad / truncate to 3 digits so `.5` and `.500` mean the same.
            val fracPart = secStr.substring(dotIdx + 1).padEnd(3, '0').take(3)
            frac = fracPart.toIntOrNull() ?: return null
        } else {
            whole = secStr.toIntOrNull() ?: return null
            frac = 0
        }
        return ((h * 3600L + m * 60L + whole) * 1000L) + frac
    }

    /**
     * Lookup the cue covering [positionMs]. Cues from [parse] are in
     * ascending time order so a binary search is correct. Returns null
     * when the position falls past the end of the last cue (e.g. a
     * 60 s movie's trickplay only covers 0..58 s).
     */
    fun cueAt(cues: List<TrickplayCue>, positionMs: Long): TrickplayCue? {
        if (cues.isEmpty()) return null
        // Binary search by start time.
        var lo = 0
        var hi = cues.size - 1
        while (lo <= hi) {
            val mid = (lo + hi) ushr 1
            val c = cues[mid]
            when {
                positionMs < c.startMs -> hi = mid - 1
                positionMs >= c.endMs -> lo = mid + 1
                else -> return c
            }
        }
        return null
    }
}
