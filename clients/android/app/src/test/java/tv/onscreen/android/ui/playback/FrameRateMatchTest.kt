package tv.onscreen.android.ui.playback

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class FrameRateMatchTest {

    private var nextId = 1
    private fun mode(hz: Float, w: Int = 3840, h: Int = 2160) = DisplayModeSpec(nextId++, w, h, hz)

    // A typical 4K TV's mode list, as displays report the NTSC rates.
    private val m60 = mode(60f)
    private val m5994 = mode(59.94006f)
    private val m50 = mode(50f)
    private val m30 = mode(30f)
    private val m2997 = mode(29.97003f)
    private val m25 = mode(25f)
    private val m24 = mode(24f)
    private val m23976 = mode(23.976025f)
    private val tv = listOf(m60, m5994, m50, m30, m2997, m25, m24, m23976)

    @Test
    fun `film goes to its own rate`() {
        assertThat(FrameRateMatch.pick(23.976f, m60, tv)).isEqualTo(m23976)
        assertThat(FrameRateMatch.pick(24f, m60, tv)).isEqualTo(m24)
        assertThat(FrameRateMatch.pick(24f, m50, tv)).isEqualTo(m24)
    }

    @Test
    fun `the NTSC rates are not their round neighbours`() {
        // 23.976 fps on 24 Hz slips a frame every 42 s: the true rate wins.
        assertThat(FrameRateMatch.pick(23.976f, m24, tv)).isEqualTo(m23976)
        assertThat(FrameRateMatch.pick(24f, m23976, tv)).isEqualTo(m24)
        assertThat(FrameRateMatch.pick(29.97f, m60, tv)).isEqualTo(m5994)
    }

    @Test
    fun `a mode that already shows it evenly stays`() {
        assertThat(FrameRateMatch.pick(30f, m60, tv)).isNull()
        assertThat(FrameRateMatch.pick(59.94f, m5994, tv)).isNull()
        assertThat(FrameRateMatch.pick(25f, m50, tv)).isNull()
        val m120 = mode(120f)
        assertThat(FrameRateMatch.pick(24f, m120, tv + m120)).isNull()
    }

    @Test
    fun `film at its own rate, faster video at 48 Hz or more`() {
        val m48 = mode(48f)
        val m120 = mode(120f)
        // Film: the lowest multiple, 24 Hz first.
        assertThat(FrameRateMatch.pick(24f, m60, listOf(m60, m120, m48, m24))).isEqualTo(m24)
        assertThat(FrameRateMatch.pick(24f, m60, listOf(m60, m120, m48))).isEqualTo(m48)
        // 25p and 30p: 50 and 60 Hz rather than the rarer 25 and 30 Hz modes.
        assertThat(FrameRateMatch.pick(25f, m60, tv)).isEqualTo(m50)
        assertThat(FrameRateMatch.pick(30f, m5994, tv)).isEqualTo(m60)
        assertThat(FrameRateMatch.pick(50f, m60, listOf(m60, m50))).isEqualTo(m50)
        // Only a 25 Hz mode fits: still better than 60 Hz judder.
        assertThat(FrameRateMatch.pick(25f, m60, listOf(m60, m25))).isEqualTo(m25)
        // Low rates (12 fps animation): already even at 60 Hz (5x), else the
        // lowest multiple.
        assertThat(FrameRateMatch.pick(12f, m60, listOf(m60, m50, m24))).isNull()
        assertThat(FrameRateMatch.pick(12f, m50, listOf(m60, m50, m24))).isEqualTo(m24)
    }

    @Test
    fun `a near rate only when there is no exact one`() {
        // A TV with 24 but no 23.976 Hz: 24 Hz still beats 60 Hz judder.
        val noNtsc = listOf(m60, m50, m24)
        assertThat(FrameRateMatch.pick(23.976f, m60, noNtsc)).isEqualTo(m24)
        // Already on the near rate, with nothing exact: stay.
        assertThat(FrameRateMatch.pick(23.976f, m24, noNtsc)).isNull()
        assertThat(FrameRateMatch.pick(59.94f, m60, listOf(m60, m50))).isNull()
    }

    @Test
    fun `the resolution stays as it is`() {
        val uhd60 = mode(60f)
        val hd24 = mode(24f, 1920, 1080)
        val uhd24 = mode(24f)
        assertThat(FrameRateMatch.pick(24f, uhd60, listOf(uhd60, hd24, uhd24))).isEqualTo(uhd24)
        // Only a lower resolution fits: no switch rather than a resolution change.
        assertThat(FrameRateMatch.pick(24f, uhd60, listOf(uhd60, hd24))).isNull()
    }

    @Test
    fun `nothing fits or the rate is unknown`() {
        assertThat(FrameRateMatch.pick(24f, m60, listOf(m60, m50))).isNull()
        assertThat(FrameRateMatch.pick(0f, m60, tv)).isNull()
        assertThat(FrameRateMatch.pick(-1f, m60, tv)).isNull() // Format.NO_VALUE
        assertThat(FrameRateMatch.pick(Float.NaN, m60, tv)).isNull()
        assertThat(FrameRateMatch.pick(1000f, m60, tv)).isNull()
    }

    @Test
    fun `suits tells an even mode from one nothing better fits`() {
        assertThat(FrameRateMatch.suits(30f, m60)).isTrue()
        assertThat(FrameRateMatch.suits(23.976f, m24)).isTrue() // near
        assertThat(FrameRateMatch.suits(23.976f, m23976)).isTrue()
        // 120 fps on a TV without 120 Hz: pick() stays, but 23.976 Hz left
        // from the last video doesn't suit it.
        assertThat(FrameRateMatch.pick(120f, m23976, tv)).isNull()
        assertThat(FrameRateMatch.suits(120f, m23976)).isFalse()
        assertThat(FrameRateMatch.suits(-1f, m60)).isFalse()
    }

    @Test
    fun `whole multiples within tolerance`() {
        assertThat(FrameRateMatch.fit(119.88f, 23.976f, 0.0005)).isEqualTo(5)
        assertThat(FrameRateMatch.fit(60f, 24f, 0.0005)).isNull() // 2.5: 3:2 pull-down
        assertThat(FrameRateMatch.fit(24f, 23.976f, 0.0005)).isNull()
        assertThat(FrameRateMatch.fit(24f, 23.976f, 0.0015)).isEqualTo(1)
        assertThat(FrameRateMatch.fit(10f, 24f, 0.0015)).isNull()
    }
}
