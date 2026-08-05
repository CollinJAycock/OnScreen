package tv.onscreen.android.ui.playback

import android.net.Uri
import androidx.media3.common.C
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DataSpec
import androidx.media3.datasource.TransferListener

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
     * Shift every cue in [vtt] earlier by [offsetMs]. Cues that end at or
     * before the new zero are dropped; cues straddling it are clamped to
     * start at zero. Non-cue lines (header, notes, styling, cue settings
     * after the arrow) pass through untouched.
     */
    fun shiftWebVtt(vtt: String, offsetMs: Long): String {
        if (offsetMs <= 0) return vtt
        val out = StringBuilder(vtt.length)
        var dropUntilBlank = false
        for (line in vtt.lineSequence()) {
            if (dropUntilBlank) {
                if (line.isBlank()) dropUntilBlank = false
                continue
            }
            if (!line.contains("-->")) {
                out.append(line).append('\n')
                continue
            }
            val matches = TIMESTAMP.findAll(line).toList()
            if (matches.size < 2) {
                out.append(line).append('\n')
                continue
            }
            val start = parseMs(matches[0]) - offsetMs
            val end = parseMs(matches[1]) - offsetMs
            if (end <= 0) {
                // Entire cue predates the resume point — drop it AND its
                // payload lines (everything up to the next blank line), or
                // the orphaned text would attach to the following cue.
                dropUntilBlank = true
                continue
            }
            val shifted = line
                .replaceRange(matches[1].range, formatMs(end))
                .replaceRange(matches[0].range, formatMs(start.coerceAtLeast(0)))
            out.append(shifted).append('\n')
        }
        return out.toString()
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
        return "%02d:%02d:%02d.%03d".format(h, min, s, frac)
    }
}

/**
 * [DataSource] that loads the whole upstream VTT (they're tens of KB), runs
 * [SubtitleShift.shiftWebVtt], and serves the shifted bytes. Wrapped around
 * the HLS side-load factory only when the session has a non-zero offset.
 */
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
}
