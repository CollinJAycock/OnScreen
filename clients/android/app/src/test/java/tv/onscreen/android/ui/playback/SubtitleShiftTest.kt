package tv.onscreen.android.ui.playback

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
    """.trimIndent() + "\n"

    @Test
    fun `zero offset is a passthrough`() {
        assertThat(SubtitleShift.shiftWebVtt(vtt, 0)).isEqualTo(vtt)
    }

    @Test
    fun `cues shift earlier by the offset and dead cues drop with their payload`() {
        // Resume 45 minutes in — the exact scenario from the field: the
        // unshifted VTT showed dialogue from 45 minutes earlier.
        val shifted = SubtitleShift.shiftWebVtt(vtt, 45 * 60_000L)

        // Cue 1 ended long before the resume point: gone, including its text.
        assertThat(shifted).doesNotContain("Opening line")
        // Cue 2 straddles: clamped to start at zero, end preserved, cue
        // settings after the arrow untouched.
        assertThat(shifted).contains("00:00:00.000 --> 00:00:03.000 position:50%")
        assertThat(shifted).contains("Straddles the resume point")
        // Cue 3: shifted by exactly the offset (1:00:00 - 45:00 = 15:00).
        assertThat(shifted).contains("00:15:00.000 --> 00:15:04.250")
        assertThat(shifted).contains("After the resume point")
        // Header intact.
        assertThat(shifted).startsWith("WEBVTT")
    }

    @Test
    fun `short MM SS timestamps parse and re-emit`() {
        val short = "WEBVTT\n\n05:10.000 --> 05:12.000\nHi\n"
        val shifted = SubtitleShift.shiftWebVtt(short, 60_000L)
        assertThat(shifted).contains("00:04:10.000 --> 00:04:12.000")
    }

    @Test
    @androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
    fun `oversized subtitle is refused instead of buffered without bound`() {
        // Upstream that never ends: without the cap, open() would buffer until
        // the heap ran out (OOM on a low-RAM stick). With it, open() throws an
        // IOException, which the side-load source treats as end-of-stream.
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
        // Stopped at (about) the cap rather than reading forever.
        assertThat(served).isAtMost(ShiftedVttDataSource.MAX_VTT_BYTES + 64 * 1024)
    }
}
