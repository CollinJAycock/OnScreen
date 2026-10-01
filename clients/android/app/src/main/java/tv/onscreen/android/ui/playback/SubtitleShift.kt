package tv.onscreen.android.ui.playback

import android.net.Uri
import androidx.media3.common.C
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DataSpec
import androidx.media3.datasource.TransferListener
import androidx.media3.common.util.UnstableApi

/**
 * Re-bases WebVTT cue timestamps for a resumed HLS session.
 *
 * The server's `/media/subtitles/{fileId}/{index}` VTT is extracted once per
 * (file, stream) and cached, so its cue times are absolute CONTENT time. A
 * resumed transcode session's timeline, however, starts at zero at the resume
 * point — the whole codebase converts with `contentTime = playerPosition +
 * hlsOffsetMs`. Side-loading the cached VTT unshifted therefore showed cues
 * from `hlsOffsetMs` EARLIER (resume a film 45 minutes in and the dialogue on
 * screen was from the opening scene). Shifting client-side keeps the server's
 * one-entry-per-stream cache intact — an offset query param would fragment it
 * per resume position and re-demux the whole source (25–60 s for a 4K remux)
 * on every resume.
 */
object SubtitleShift {

    // Matches both timestamp shapes VTT allows: HH:MM:SS.mmm and MM:SS.mmm.
    private val TIMESTAMP = Regex("""(?:(\d+):)?([0-5]\d):([0-5]\d)\.(\d{3})""")

    /**
     * Shift every cue in [vtt] earlier by [offsetMs]. A cue that ends at or
     * before the new zero is dropped whole, block by block: its id line too,
     * which the line-by-line version left behind (one stray line per dropped
     * cue, run together ahead of the first cue kept). One straddling the new
     * zero is clamped to start there. Blocks without a timing line (the
     * header, NOTE, STYLE) pass through untouched, as do the cue settings
     * after the arrow. The phone client's SubtitleShift, ported back.
     */
    fun shiftWebVtt(vtt: String, offsetMs: Long): String {
        if (offsetMs <= 0) return vtt
        val out = StringBuilder(vtt.length)
        val block = mutableListOf<String>()
        var atStart = true
        fun flush() {
            if (block.isEmpty()) return
            // The "WEBVTT" line is a block of its own. A first cue run
            // straight on from it, with no blank line between, made one
            // block with it, and dropping that cue dropped the header too:
            // what was left no longer parsed as WebVTT at all.
            if (atStart && isHeader(block[0]) && block.drop(1).any { it.contains("-->") }) {
                out.append(block.removeAt(0)).append("\n\n")
            }
            atStart = false
            val timingAt = block.indexOfFirst { it.contains("-->") }
            if (timingAt < 0) {
                block.forEach { out.append(it).append('\n') }
            } else {
                val timing = shiftTiming(block[timingAt], offsetMs)
                if (timing != null) {
                    block.forEachIndexed { i, line ->
                        out.append(if (i == timingAt) timing else line).append('\n')
                    }
                }
            }
            block.clear()
        }
        for (line in vtt.lineSequence()) {
            if (line.isBlank()) {
                flush()
                out.append('\n')
            } else {
                block += line
            }
        }
        flush()
        return out.toString()
    }

    /** The file's "WEBVTT" line, after a byte-order mark if there is one. */
    private fun isHeader(line: String): Boolean = line.removePrefix("\uFEFF").startsWith("WEBVTT")

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
 * [DataSource] that loads the whole upstream VTT (they're tens of KB), runs
 * [SubtitleShift.shiftWebVtt], and serves the shifted bytes. Wrapped around
 * the HLS side-load factory only when the session has a non-zero offset.
 */
// @UnstableApi, not @OptIn: this class IMPLEMENTS media3's DataSource, and
// Kotlin requires the marker rather than a local opt-in when a SUPERTYPE
// carries the opt-in requirement. Its call site opts in.
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
        // Open the FULL resource regardless of the requested range — the
        // shift changes byte offsets, so ranges into the original are
        // meaningless against the shifted output.
        val fullSpec = dataSpec.buildUpon().setPosition(0).setLength(C.LENGTH_UNSET.toLong()).build()
        upstream.open(fullSpec)
        val buf = java.io.ByteArrayOutputStream()
        val chunk = ByteArray(64 * 1024)
        while (true) {
            val n = upstream.read(chunk, 0, chunk.size)
            if (n == C.RESULT_END_OF_INPUT || n < 0) break
            // Hard cap: this buffers the whole file and then builds a shifted
            // String copy (~3x the size on the heap). Real VTTs are tens of KB;
            // an oversized/pathological sidecar would otherwise OOM the player
            // on a low-RAM Fire TV stick every time the title resumes. Throwing
            // here drops only the text track — the side-load source is built
            // with setTreatLoadErrorsAsEndOfStream(true).
            if (buf.size().toLong() + n > MAX_VTT_BYTES) {
                throw java.io.IOException("subtitle exceeds ${MAX_VTT_BYTES} bytes; not loading")
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

    companion object {
        /** Upper bound on a side-loaded VTT we will buffer and shift. */
        const val MAX_VTT_BYTES: Long = 16L * 1024 * 1024
    }

    override fun close() {
        data = null
        upstream.close()
    }
}
