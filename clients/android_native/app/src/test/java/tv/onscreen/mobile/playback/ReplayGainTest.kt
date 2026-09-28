package tv.onscreen.mobile.playback

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.ItemFile
import kotlin.math.pow

/**
 * Mirrors web/src/lib/replaygain.test.ts case for case, so the phone and the
 * web player apply the same gain for the same tags + setting.
 */
class ReplayGainTest {

    private fun db(x: Double) = 10.0.pow(x / 20)
    private val eps = 1e-10

    private val tagged = ReplayGainInfo(trackGain = -6.0, trackPeak = 0.5, albumGain = -8.0, albumPeak = 0.6)

    // ── replayGainLinear ────────────────────────────────────────────────

    @Test
    fun `is unity when the mode is off, whatever the tags and preamp`() {
        assertThat(ReplayGain.linear(tagged, ReplayGainMode.OFF, 0.0)).isEqualTo(1.0)
        assertThat(ReplayGain.linear(tagged, ReplayGainMode.OFF, 12.0)).isEqualTo(1.0)
    }

    @Test
    fun `applies the track gain in track mode`() {
        assertThat(ReplayGain.linear(tagged, ReplayGainMode.TRACK, 0.0)).isWithin(eps).of(db(-6.0))
    }

    @Test
    fun `applies the album gain in album mode`() {
        assertThat(ReplayGain.linear(tagged, ReplayGainMode.ALBUM, 0.0)).isWithin(eps).of(db(-8.0))
    }

    @Test
    fun `adds the preamp to the tag gain`() {
        assertThat(ReplayGain.linear(tagged, ReplayGainMode.TRACK, 3.0)).isWithin(eps).of(db(-3.0))
        assertThat(ReplayGain.linear(tagged, ReplayGainMode.TRACK, -4.5)).isWithin(eps).of(db(-10.5))
    }

    @Test
    fun `clamps the preamp to plus-minus 15 dB like the native engine`() {
        val quiet = ReplayGainInfo(trackGain = -30.0, trackPeak = 0.01)
        assertThat(ReplayGain.linear(quiet, ReplayGainMode.TRACK, 40.0)).isWithin(eps).of(db(-15.0))
        assertThat(ReplayGain.linear(quiet, ReplayGainMode.TRACK, -40.0)).isWithin(eps).of(db(-45.0))
        assertThat(ReplayGain.clampPreamp(99.0)).isEqualTo(15.0)
        assertThat(ReplayGain.clampPreamp(-99.0)).isEqualTo(-15.0)
        assertThat(ReplayGain.clampPreamp(Double.NaN)).isEqualTo(0.0)
    }

    // ── clipping cap ────────────────────────────────────────────────────

    @Test
    fun `caps a boost so peak times gain stays at or under 1`() {
        // +6 dB (x1.995) on a 0.8 peak would reach 1.6 — capped to 1/0.8.
        val g = ReplayGain.linear(ReplayGainInfo(trackGain = 6.0, trackPeak = 0.8), ReplayGainMode.TRACK, 0.0)
        assertThat(g).isWithin(eps).of(1.25)
        assertThat(0.8 * g).isAtMost(1 + 1e-12)
    }

    @Test
    fun `leaves a boost alone when the peak has headroom`() {
        // +3 dB (x1.413) on a 0.5 peak lands at 0.707 — no cap.
        assertThat(ReplayGain.linear(ReplayGainInfo(trackGain = 3.0, trackPeak = 0.5), ReplayGainMode.TRACK, 0.0))
            .isWithin(eps).of(db(3.0))
    }

    @Test
    fun `caps the preamp boost too`() {
        val g = ReplayGain.linear(ReplayGainInfo(trackGain = -2.0, trackPeak = 0.9), ReplayGainMode.TRACK, 6.0)
        assertThat(0.9 * g).isWithin(eps).of(1.0)
    }

    @Test
    fun `attenuates a file whose peak is already over full scale`() {
        // Lossy decodes can report peaks > 1; -1 dB alone would still clip.
        val g = ReplayGain.linear(ReplayGainInfo(trackGain = -1.0, trackPeak = 1.2), ReplayGainMode.TRACK, 0.0)
        assertThat(g).isWithin(eps).of(1 / 1.2)
    }

    @Test
    fun `uses the album peak in album mode`() {
        // +6 dB album gain, album peak 0.9 -> capped at 1/0.9 even though
        // this track's own peak (0.5) would have allowed the full boost.
        val g = ReplayGain.linear(
            ReplayGainInfo(trackGain = 0.0, trackPeak = 0.5, albumGain = 6.0, albumPeak = 0.9),
            ReplayGainMode.ALBUM,
            0.0,
        )
        assertThat(g).isWithin(eps).of(1 / 0.9)
    }

    @Test
    fun `treats an unknown peak as full scale - never boosts past unity`() {
        assertThat(ReplayGain.linear(ReplayGainInfo(trackGain = 4.0), ReplayGainMode.TRACK, 0.0)).isEqualTo(1.0)
        assertThat(ReplayGain.linear(ReplayGainInfo(trackGain = -3.0), ReplayGainMode.TRACK, 0.0))
            .isWithin(eps).of(db(-3.0))
        assertThat(ReplayGain.linear(ReplayGainInfo(trackGain = -3.0, trackPeak = 0.0), ReplayGainMode.TRACK, 6.0))
            .isEqualTo(1.0)
    }

    // ── mode fallback ───────────────────────────────────────────────────

    @Test
    fun `album mode falls back to the track tags when there is no album gain`() {
        val single = ReplayGainInfo(trackGain = -5.0, trackPeak = 0.7)
        assertThat(ReplayGain.linear(single, ReplayGainMode.ALBUM, 0.0)).isWithin(eps).of(db(-5.0))
        assertThat(ReplayGain.select(single, ReplayGainMode.ALBUM)).isEqualTo(ReplayGainSelection(-5.0, 0.7))
    }

    @Test
    fun `album gain without an album peak borrows the track peak as the clip guard`() {
        assertThat(
            ReplayGain.select(ReplayGainInfo(trackGain = 0.0, trackPeak = 0.95, albumGain = 2.0), ReplayGainMode.ALBUM),
        ).isEqualTo(ReplayGainSelection(2.0, 0.95))
    }

    @Test
    fun `track mode does not fall back to album tags`() {
        assertThat(ReplayGain.linear(ReplayGainInfo(albumGain = -7.0, albumPeak = 0.5), ReplayGainMode.TRACK, 0.0))
            .isEqualTo(1.0)
        assertThat(ReplayGain.select(ReplayGainInfo(albumGain = -7.0), ReplayGainMode.TRACK)).isNull()
    }

    // ── missing tags ────────────────────────────────────────────────────

    @Test
    fun `plays untagged files at unity - the preamp is not applied on its own`() {
        assertThat(ReplayGain.linear(ReplayGainInfo(), ReplayGainMode.TRACK, 6.0)).isEqualTo(1.0)
        assertThat(ReplayGain.linear(ReplayGainInfo(), ReplayGainMode.ALBUM, -6.0)).isEqualTo(1.0)
        assertThat(ReplayGain.linear(null, ReplayGainMode.TRACK, 6.0)).isEqualTo(1.0)
        assertThat(ReplayGain.linear(null, ReplayGainMode.ALBUM, 6.0)).isEqualTo(1.0)
    }

    @Test
    fun `ignores non-finite tag values`() {
        assertThat(ReplayGain.linear(ReplayGainInfo(trackGain = Double.NaN, trackPeak = 0.5), ReplayGainMode.TRACK, 0.0))
            .isEqualTo(1.0)
        val g = ReplayGain.linear(
            ReplayGainInfo(trackGain = -6.0, trackPeak = Double.POSITIVE_INFINITY),
            ReplayGainMode.TRACK,
            0.0,
        )
        assertThat(g).isWithin(eps).of(db(-6.0))
    }

    // ── replayGainFromFile ──────────────────────────────────────────────

    private fun file(tg: Double? = null, tp: Double? = null, ag: Double? = null, ap: Double? = null) =
        ItemFile(
            id = "f", stream_url = "/media/stream/f",
            replaygain_track_gain = tg, replaygain_track_peak = tp,
            replaygain_album_gain = ag, replaygain_album_peak = ap,
        )

    @Test
    fun `fromFile maps the ItemFile replaygain fields`() {
        assertThat(ReplayGain.fromFile(file(-7.2, 0.98, -8.1, 0.99)))
            .isEqualTo(ReplayGainInfo(trackGain = -7.2, trackPeak = 0.98, albumGain = -8.1, albumPeak = 0.99))
    }

    @Test
    fun `fromFile returns an empty info for a file without tags or no file`() {
        assertThat(ReplayGain.fromFile(file())).isEqualTo(ReplayGainInfo.NONE)
        assertThat(ReplayGain.fromFile(null)).isEqualTo(ReplayGainInfo.NONE)
    }

    @Test
    fun `fromFile keeps partial tags and drops non-finite ones`() {
        assertThat(ReplayGain.fromFile(file(tg = 1.5))).isEqualTo(ReplayGainInfo(trackGain = 1.5))
        assertThat(ReplayGain.fromFile(file(tg = Double.NaN, tp = 0.5))).isEqualTo(ReplayGainInfo(trackPeak = 0.5))
    }

    // ── mode wire + settings grid (phone-specific) ──────────────────────

    @Test
    fun `mode round-trips its wire value and defaults to off`() {
        ReplayGainMode.entries.forEach { assertThat(ReplayGainMode.fromWire(it.wire)).isEqualTo(it) }
        assertThat(ReplayGainMode.fromWire(null)).isEqualTo(ReplayGainMode.OFF)
        assertThat(ReplayGainMode.fromWire("loud")).isEqualTo(ReplayGainMode.OFF)
    }

    @Test
    fun `settings preamp snaps to the half-dB grid inside plus-minus 6`() {
        assertThat(ReplayGain.snapUiPreamp(0.0)).isEqualTo(0.0)
        assertThat(ReplayGain.snapUiPreamp(1.26)).isEqualTo(1.5)
        assertThat(ReplayGain.snapUiPreamp(-2.2)).isEqualTo(-2.0)
        assertThat(ReplayGain.snapUiPreamp(9.0)).isEqualTo(6.0)
        assertThat(ReplayGain.snapUiPreamp(-9.0)).isEqualTo(-6.0)
        assertThat(ReplayGain.snapUiPreamp(Double.NaN)).isEqualTo(0.0)
    }
}
