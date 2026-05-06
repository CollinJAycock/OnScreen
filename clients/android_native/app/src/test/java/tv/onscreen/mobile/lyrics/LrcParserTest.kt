package tv.onscreen.mobile.lyrics

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class LrcParserTest {

    @Test
    fun `parses a basic LRC body`() {
        val body = """
            [ar:Radiohead]
            [ti:Let Down]
            [00:12.50]Transport, motorways and tramlines
            [00:18.20]Starting and then stopping
        """.trimIndent()
        val cues = LrcParser.parse(body)
        // Metadata lines [ar:] / [ti:] don't match the timestamp
        // regex (mm:ss expected, not characters), so they're dropped.
        assertThat(cues).hasSize(2)
        assertThat(cues[0].timeMs).isEqualTo(12_500)
        assertThat(cues[0].text).isEqualTo("Transport, motorways and tramlines")
        assertThat(cues[1].timeMs).isEqualTo(18_200)
    }

    @Test
    fun `parses millisecond fractions of varying widths`() {
        val body = """
            [00:00.5]half-second
            [00:01.50]half-second
            [00:02.500]half-second
            [00:03]no fraction
        """.trimIndent()
        val cues = LrcParser.parse(body)
        assertThat(cues.map { it.timeMs }).containsExactly(500L, 1_500L, 2_500L, 3_000L).inOrder()
    }

    @Test
    fun `expands lines with multiple timestamps`() {
        // The chorus repeats — LRC writers stack timestamps on the
        // same line so they don't have to duplicate the lyric body.
        val body = "[00:30.00][01:00.00][01:30.00]Chorus line"
        val cues = LrcParser.parse(body)
        assertThat(cues.map { it.timeMs }).containsExactly(30_000L, 60_000L, 90_000L).inOrder()
        assertThat(cues.all { it.text == "Chorus line" }).isTrue()
    }

    @Test
    fun `empty-text instrumental cues survive parsing`() {
        // LRC convention: a timestamped line with no text marks an
        // intro / interlude. UI renders these as a faded music note.
        val body = "[00:05.00]"
        val cues = LrcParser.parse(body)
        assertThat(cues).hasSize(1)
        assertThat(cues[0].text).isEmpty()
        assertThat(cues[0].timeMs).isEqualTo(5_000)
    }

    @Test
    fun `unsorted input is sorted on parse`() {
        // Some LRC files have stack-of-timestamps in non-monotonic
        // order; cueAt's binary search relies on sorted input. Lock
        // down the parse-time sort so callers don't have to.
        val body = """
            [01:00.00]B
            [00:30.00]A
            [01:30.00]C
        """.trimIndent()
        val cues = LrcParser.parse(body)
        assertThat(cues.map { it.timeMs }).containsExactly(30_000L, 60_000L, 90_000L).inOrder()
        assertThat(cues.map { it.text }).containsExactly("A", "B", "C").inOrder()
    }

    @Test
    fun `whitespace and blank lines are tolerated`() {
        val body = """

            [00:01.00]One

            [00:02.00]Two

        """.trimIndent()
        assertThat(LrcParser.parse(body)).hasSize(2)
    }

    @Test
    fun `metadata-only files yield no cues`() {
        val body = """
            [ar:Some Artist]
            [ti:Some Title]
            [length:3:21]
        """.trimIndent()
        assertThat(LrcParser.parse(body)).isEmpty()
    }

    @Test
    fun `cueAt returns null before the first cue`() {
        val cues = LrcParser.parse("[00:30.00]Hi")
        // Position 0 (start of track) — first cue is at 30s. The intro
        // hasn't started yet; the overlay shows no line.
        assertThat(LrcParser.cueAt(cues, 0)).isNull()
        assertThat(LrcParser.cueAt(cues, 29_999)).isNull()
    }

    @Test
    fun `cueAt returns the active cue and advances`() {
        val cues = LrcParser.parse(
            """
            [00:00.00]One
            [00:05.00]Two
            [00:10.00]Three
            """.trimIndent(),
        )
        assertThat(LrcParser.cueAt(cues, 0)?.text).isEqualTo("One")
        assertThat(LrcParser.cueAt(cues, 4_999)?.text).isEqualTo("One")
        assertThat(LrcParser.cueAt(cues, 5_000)?.text).isEqualTo("Two")
        assertThat(LrcParser.cueAt(cues, 9_999)?.text).isEqualTo("Two")
        assertThat(LrcParser.cueAt(cues, 10_000)?.text).isEqualTo("Three")
        // Past the last cue: the last line "stays" until track end.
        assertThat(LrcParser.cueAt(cues, 999_999)?.text).isEqualTo("Three")
    }

    @Test
    fun `cueAt handles empty input`() {
        assertThat(LrcParser.cueAt(emptyList(), 0)).isNull()
    }
}
