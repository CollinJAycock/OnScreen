package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import org.junit.Assert.assertThrows
import org.junit.Test

class SubtitleShiftTest {

    private val vtt = """
        WEBVTT

        1
        00:00:10.000 --> 00:00:12.500
        Opening line

        2
        00:44:58.000 --> 00:45:03.000 position:50%
        Straddles the resume point

        3
        01:00:00.000 --> 01:00:04.250
        After the resume point
        second line
    """.trimIndent() + "\n"

    @Test
    fun `zero offset is a passthrough`() {
        assertThat(SubtitleShift.shiftWebVtt(vtt, 0)).isEqualTo(vtt)
    }

    @Test
    fun `cues shift earlier by the offset and dead cues drop whole`() {
        // Resume 45 minutes in: unshifted, the dialogue on screen was from 45
        // minutes earlier in the film.
        val shifted = SubtitleShift.shiftWebVtt(vtt, 45 * 60_000L)

        // Cue 1 ended long before the resume point: gone, id and text too.
        assertThat(shifted).doesNotContain("Opening line")
        assertThat(shifted.lines()).doesNotContain("1")
        // Cue 2 straddles: clamped to start at zero, end kept, cue settings
        // after the arrow untouched, its id kept.
        assertThat(shifted).contains("2\n00:00:00.000 --> 00:00:03.000 position:50%\nStraddles the resume point")
        // Cue 3: shifted by exactly the offset (1:00:00 − 45:00 = 15:00),
        // both payload lines kept.
        assertThat(shifted).contains("00:15:00.000 --> 00:15:04.250\nAfter the resume point\nsecond line")
        assertThat(shifted).startsWith("WEBVTT\n")
    }

    @Test
    fun `a dropped first cue right under the header leaves the header`() {
        // No blank line between the header and the first cue: they were one
        // block, so dropping the cue dropped "WEBVTT" too, and a file without
        // it does not parse — the track came up empty.
        val tight = "WEBVTT\n00:00:01.000 --> 00:00:02.000\nGone\n\n00:01:00.000 --> 00:01:02.000\nKept\n"
        val shifted = SubtitleShift.shiftWebVtt(tight, 30_000L)
        assertThat(shifted).startsWith("WEBVTT\n\n00:00:30.000 --> 00:00:32.000\nKept\n")
        assertThat(shifted).doesNotContain("Gone")
        // Behind a byte-order mark as well.
        val bom = Char(0xFEFF)
        assertThat(SubtitleShift.shiftWebVtt("$bom$tight", 30_000L))
            .startsWith("${bom}WEBVTT\n\n00:00:30.000 --> 00:00:32.000\nKept\n")
    }

    @Test
    fun `a kept first cue right under the header is set apart from it`() {
        // Media3 reads everything up to the first blank line as header, so
        // without one the cue would be read as header and never show.
        val tight = "WEBVTT\n1\n00:01:00.000 --> 00:01:02.000\nKept\n"
        assertThat(SubtitleShift.shiftWebVtt(tight, 30_000L))
            .startsWith("WEBVTT\n\n1\n00:00:30.000 --> 00:00:32.000\nKept\n")
    }

    @Test
    fun `header lines under the header stay with it`() {
        val withMeta = "WEBVTT\nKind: captions\nLanguage: en\n\n00:01:00.000 --> 00:01:02.000\nHi\n"
        assertThat(SubtitleShift.shiftWebVtt(withMeta, 30_000L))
            .startsWith("WEBVTT\nKind: captions\nLanguage: en\n\n00:00:30.000 --> 00:00:32.000\nHi\n")
    }

    @Test
    fun `short MM SS timestamps parse and re-emit`() {
        val short = "WEBVTT\n\n05:10.000 --> 05:12.000\nHi\n"
        assertThat(SubtitleShift.shiftWebVtt(short, 60_000L)).contains("00:04:10.000 --> 00:04:12.000\nHi")
    }

    @Test
    fun `CRLF input shifts too`() {
        val crlf = "WEBVTT\r\n\r\n00:01:00.000 --> 00:01:02.000\r\nHello\r\n"
        assertThat(SubtitleShift.shiftWebVtt(crlf, 30_000L)).contains("00:00:30.000 --> 00:00:32.000\nHello")
    }

    @Test
    fun `oversized subtitle is refused instead of buffered without bound`() {
        // Upstream that never ends: without the cap, open() would buffer until
        // the heap ran out. With it, open() throws an IOException, which the
        // side-load source treats as end-of-stream (an empty track).
        var served = 0L
        val endless = object : androidx.media3.datasource.DataSource {
            override fun addTransferListener(transferListener: androidx.media3.datasource.TransferListener) {}
            override fun open(dataSpec: androidx.media3.datasource.DataSpec): Long =
                androidx.media3.common.C.LENGTH_UNSET.toLong()
            override fun read(buffer: ByteArray, offset: Int, length: Int): Int {
                java.util.Arrays.fill(buffer, offset, offset + length, 'a'.code.toByte())
                served += length
                return length
            }
            override fun getUri(): android.net.Uri? = null
            override fun close() {}
        }
        val ds = ShiftedVttDataSource(endless, 60_000L)
        val spec = androidx.media3.datasource.DataSpec.Builder()
            .setUri(io.mockk.mockk<android.net.Uri>(relaxed = true))
            .build()
        assertThrows(java.io.IOException::class.java) { ds.open(spec) }
        assertThat(served).isAtMost(ShiftedVttDataSource.MAX_VTT_BYTES + 64 * 1024)
    }
}
