package tv.onscreen.mobile.trickplay

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Unit tests for [TrickplayVtt]. The VTT format is well-specified but
 * unforgiving on edge cases — these lock down:
 *   - happy path: 10 s cues with the OnScreen-shape `xywh` payload
 *   - timestamp formats: H:MM:SS.mmm + MM:SS.mmm; fractional digits
 *   - tolerance: header / NOTE / malformed cues are skipped
 *   - cueAt binary search: finds the right cue, returns null past end
 */
class TrickplayVttTest {

    @Test
    fun `parses a simple two-cue VTT`() {
        val body = """
            WEBVTT

            00:00:00.000 --> 00:00:10.000
            sprite_000.jpg#xywh=0,0,320,180

            00:00:10.000 --> 00:00:20.000
            sprite_000.jpg#xywh=320,0,320,180
        """.trimIndent()
        val cues = TrickplayVtt.parse(body)
        assertThat(cues).hasSize(2)
        assertThat(cues[0].startMs).isEqualTo(0)
        assertThat(cues[0].endMs).isEqualTo(10000)
        assertThat(cues[0].file).isEqualTo("sprite_000.jpg")
        assertThat(cues[0].x).isEqualTo(0)
        assertThat(cues[0].y).isEqualTo(0)
        assertThat(cues[0].w).isEqualTo(320)
        assertThat(cues[0].h).isEqualTo(180)
        assertThat(cues[1].x).isEqualTo(320)
    }

    @Test
    fun `skips the WEBVTT header without confusing it for a cue`() {
        // The first stanza contains only the magic line — no " --> ".
        // parseCue returns null and the cue list starts at the second
        // stanza. If this regressed to "header swallows the next cue
        // body" we'd see only 1 cue here.
        val body = """
            WEBVTT

            00:00:00.000 --> 00:00:10.000
            sprite_000.jpg#xywh=0,0,320,180
        """.trimIndent()
        assertThat(TrickplayVtt.parse(body)).hasSize(1)
    }

    @Test
    fun `skips NOTE blocks`() {
        // VTT comments are NOTE-prefixed stanzas with no timing line.
        // Our parser doesn't special-case NOTE — it just rejects any
        // stanza without " --> ", which catches both NOTE and any
        // other free-form metadata block.
        val body = """
            WEBVTT

            NOTE this is a comment
            another line in the same note

            00:00:00.000 --> 00:00:10.000
            sprite_000.jpg#xywh=0,0,320,180
        """.trimIndent()
        assertThat(TrickplayVtt.parse(body)).hasSize(1)
    }

    @Test
    fun `accepts H_MM_SS_mmm and MM_SS_mmm timestamps`() {
        // H:MM:SS form — typical for hour-plus media.
        assertThat(TrickplayVtt.parseTime("01:23:45.678")).isEqualTo(
            (1 * 3600L + 23 * 60L + 45L) * 1000L + 678L,
        )
        // MM:SS form — VTT permits an implicit zero hour.
        assertThat(TrickplayVtt.parseTime("12:34.500")).isEqualTo((12 * 60L + 34L) * 1000L + 500L)
    }

    @Test
    fun `tolerates short or missing fractional seconds`() {
        // ".5" should mean 500 ms; ".50" → 500 ms; no fraction → 0 ms.
        assertThat(TrickplayVtt.parseTime("00:00.5")).isEqualTo(500)
        assertThat(TrickplayVtt.parseTime("00:00.50")).isEqualTo(500)
        assertThat(TrickplayVtt.parseTime("00:00")).isEqualTo(0)
    }

    @Test
    fun `rejects malformed timestamps`() {
        assertThat(TrickplayVtt.parseTime("nope")).isNull()
        assertThat(TrickplayVtt.parseTime("00")).isNull()
        assertThat(TrickplayVtt.parseTime("12:ab.500")).isNull()
        assertThat(TrickplayVtt.parseTime("00:00:00:00")).isNull()
    }

    @Test
    fun `skips cues missing the xywh fragment`() {
        // Server *should* always emit xywh, but defensive — a regressed
        // VTT generator that drops the fragment shouldn't crash the
        // player; it should just yield no thumbnails.
        val body = """
            WEBVTT

            00:00:00.000 --> 00:00:10.000
            sprite_000.jpg

            00:00:10.000 --> 00:00:20.000
            sprite_000.jpg#xywh=320,0,320,180
        """.trimIndent()
        val cues = TrickplayVtt.parse(body)
        assertThat(cues).hasSize(1)
        assertThat(cues[0].x).isEqualTo(320)
    }

    @Test
    fun `skips cues with non-numeric xywh values`() {
        val body = """
            WEBVTT

            00:00:00.000 --> 00:00:10.000
            sprite_000.jpg#xywh=0,oops,320,180
        """.trimIndent()
        assertThat(TrickplayVtt.parse(body)).isEmpty()
    }

    @Test
    fun `cueAt returns the cue containing the position`() {
        val body = """
            WEBVTT

            00:00:00.000 --> 00:00:10.000
            sprite_000.jpg#xywh=0,0,320,180

            00:00:10.000 --> 00:00:20.000
            sprite_000.jpg#xywh=320,0,320,180

            00:00:20.000 --> 00:00:30.000
            sprite_000.jpg#xywh=640,0,320,180
        """.trimIndent()
        val cues = TrickplayVtt.parse(body)
        assertThat(TrickplayVtt.cueAt(cues, 0)?.x).isEqualTo(0)
        // Inclusive on start, exclusive on end — 10000 belongs to the
        // second cue, not the first.
        assertThat(TrickplayVtt.cueAt(cues, 9999)?.x).isEqualTo(0)
        assertThat(TrickplayVtt.cueAt(cues, 10000)?.x).isEqualTo(320)
        assertThat(TrickplayVtt.cueAt(cues, 25000)?.x).isEqualTo(640)
    }

    @Test
    fun `cueAt returns null past the last cue end`() {
        // Trickplay is generated to N seconds; if the player is at
        // duration-3 because the last cue covered 0..duration-10, we
        // return null and the UI falls back to no thumbnail.
        val body = """
            WEBVTT

            00:00:00.000 --> 00:00:10.000
            sprite_000.jpg#xywh=0,0,320,180
        """.trimIndent()
        val cues = TrickplayVtt.parse(body)
        assertThat(TrickplayVtt.cueAt(cues, 10000)).isNull()
        assertThat(TrickplayVtt.cueAt(cues, 999_999)).isNull()
    }

    @Test
    fun `cueAt returns null when no cues parsed`() {
        assertThat(TrickplayVtt.cueAt(emptyList(), 0)).isNull()
    }
}
