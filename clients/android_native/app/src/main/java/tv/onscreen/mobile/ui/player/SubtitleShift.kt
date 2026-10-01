package tv.onscreen.mobile.ui.player

import android.net.Uri
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DataSpec
import androidx.media3.datasource.TransferListener

/**
 * Re-bases WebVTT cue times for a resumed HLS session.
 *
 * The server's `/media/subtitles/{fileId}/{index}` VTT is extracted once per
 * (file, stream) and cached, so its cue times are CONTENT time. A resumed
 * remux / transcode session's timeline starts at zero where the session
 * opens — the whole player converts with `content = position + hlsOffsetMs`.
 * Unshifted, the cues on screen were from `hlsOffsetMs` EARLIER in the film
 * (resume 45 minutes in, and the opening scene's dialogue plays over it).
 * Shifting here keeps the server's one-file-per-stream cache intact: an offset
 * query parameter would fragment it per resume point and re-demux the whole
 * source (25–60 s for a 4K remux) on every resume. Ported from the TV client,
 * which found and fixed this first.
 */
object SubtitleShift {

    // Both timestamp shapes WebVTT allows: HH:MM:SS.mmm and MM:SS.mmm.
    private val TIMESTAMP = Regex("""(?:(\d+):)?([0-5]\d):([0-5]\d)\.(\d{3})""")

    /**
     * Shift every cue in [vtt] earlier by [offsetMs]. A cue that ends at or
     * before the new zero is dropped whole — its id line and text too (the
     * TV version drops the timing and text line by line, leaving the id
     * behind); one straddling it is clamped to start at zero. Blocks without
     * a timing line (the header, NOTE, STYLE) pass through untouched, as do
     * the cue settings after the arrow.
     *
     * The opening "WEBVTT" line is always a block of its own and always kept.
     * A first cue written right under it, with no blank line between, used to
     * share its block — dropping that cue dropped the header, and a file
     * without one does not parse at all (the track came up empty). A cue kept
     * there gets the blank line it lacked: Media3 reads every line up to the
     * first blank one as header, so it would never have shown. Header lines
     * under it (no "-->": "Kind:", "Language:") stay attached as they were.
     */
    fun shiftWebVtt(vtt: String, offsetMs: Long): String {
        if (offsetMs <= 0) return vtt
        val out = StringBuilder(vtt.length)
        val block = mutableListOf<String>()
        // The block being collected started right under the header line.
        var underHeader = false
        fun flush() {
            if (block.isEmpty()) return
            val timingAt = block.indexOfFirst { it.contains("-->") }
            if (timingAt < 0) {
                block.forEach { out.append(it).append('\n') }
            } else {
                val timing = shiftTiming(block[timingAt], offsetMs)
                if (timing != null) {
                    if (underHeader) out.append('\n')
                    block.forEachIndexed { i, line ->
                        out.append(if (i == timingAt) timing else line).append('\n')
                    }
                }
            }
            block.clear()
        }
        for ((n, line) in vtt.lineSequence().withIndex()) {
            // (A byte-order mark may come first.)
            if (n == 0 && line.dropWhile { it.code == 0xFEFF }.startsWith("WEBVTT")) {
                out.append(line).append('\n')
                underHeader = true
            } else if (line.isBlank()) {
                flush()
                underHeader = false
                out.append('\n')
            } else {
                block += line
            }
        }
        flush()
        return out.toString()
    }

    /** The timing line shifted, or null when the cue ends before zero. A line
     *  without two timestamps is left alone. */
    private fun shiftTiming(line: String, offsetMs: Long): String? {
        val matches = TIMESTAMP.findAll(line).toList()
        if (matches.size < 2) return line
        val start = parseMs(matches[0]) - offsetMs
        val end = parseMs(matches[1]) - offsetMs
        if (end <= 0) return null
        return line
            .replaceRange(matches[1].range, formatMs(end))
            .replaceRange(matches[0].range, formatMs(start.coerceAtLeast(0)))
    }

    private fun parseMs(m: MatchResult): Long {
        val (h, min, s, ms) = m.destructured
        return (if (h.isEmpty()) 0L else h.toLong()) * 3_600_000L +
            min.toLong() * 60_000L + s.toLong() * 1_000L + ms.toLong()
    }

    private fun formatMs(ms: Long): String {
        val h = ms / 3_600_000
        val min = (ms % 3_600_000) / 60_000
        val s = (ms % 60_000) / 1_000
        val frac = ms % 1_000
        return "%02d:%02d:%02d.%03d".format(java.util.Locale.US, h, min, s, frac)
    }
}

/**
 * [DataSource] that reads the whole upstream VTT (tens of KB), runs
 * [SubtitleShift.shiftWebVtt] on it and serves the shifted bytes. Wraps the
 * side-load data source only for a session with a non-zero offset.
 */
// @UnstableApi, not @OptIn: this class IMPLEMENTS media3's DataSource, and
// Kotlin requires the marker rather than a local opt-in when a SUPERTYPE
// carries the opt-in requirement.
@UnstableApi
class ShiftedVttDataSource(
    private val upstream: DataSource,
    private val offsetMs: Long,
) : DataSource {

    private var data: ByteArray? = null
    private var position = 0
    private var uri: Uri? = null

    override fun addTransferListener(transferListener: TransferListener) {
        upstream.addTransferListener(transferListener)
    }

    override fun open(dataSpec: DataSpec): Long {
        uri = dataSpec.uri
        // Open the FULL resource whatever range was asked for: the shift moves
        // byte offsets, so a range into the original means nothing here.
        val fullSpec = dataSpec.buildUpon().setPosition(0).setLength(C.LENGTH_UNSET.toLong()).build()
        upstream.open(fullSpec)
        val buf = java.io.ByteArrayOutputStream()
        val chunk = ByteArray(64 * 1024)
        while (true) {
            val n = upstream.read(chunk, 0, chunk.size)
            if (n == C.RESULT_END_OF_INPUT || n < 0) break
            // Hard cap: this buffers the whole file, then builds a shifted
            // String copy (~3x the size on the heap). Real VTTs are tens of KB;
            // a pathological one must not run the player out of memory.
            // Throwing drops only this text track — the side-load source is
            // built with setTreatLoadErrorsAsEndOfStream(true).
            if (buf.size().toLong() + n > MAX_VTT_BYTES) {
                throw java.io.IOException("subtitle exceeds $MAX_VTT_BYTES bytes; not loading")
            }
            buf.write(chunk, 0, n)
        }
        val shifted = SubtitleShift.shiftWebVtt(buf.toString("UTF-8"), offsetMs)
            .toByteArray(Charsets.UTF_8)
        data = shifted
        position = 0
        return shifted.size.toLong()
    }

    override fun read(buffer: ByteArray, offset: Int, length: Int): Int {
        val d = data ?: return C.RESULT_END_OF_INPUT
        if (length == 0) return 0
        if (position >= d.size) return C.RESULT_END_OF_INPUT
        val n = minOf(length, d.size - position)
        System.arraycopy(d, position, buffer, offset, n)
        position += n
        return n
    }

    override fun getUri(): Uri? = uri

    override fun close() {
        data = null
        upstream.close()
    }

    companion object {
        /** Upper bound on a side-loaded VTT we will buffer and shift. */
        const val MAX_VTT_BYTES: Long = 16L * 1024 * 1024
    }
}
