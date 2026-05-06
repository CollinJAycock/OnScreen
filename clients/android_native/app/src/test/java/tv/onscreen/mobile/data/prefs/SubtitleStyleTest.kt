package tv.onscreen.mobile.data.prefs

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.prefs.SubtitleStyle.Background
import tv.onscreen.mobile.data.prefs.SubtitleStyle.Outline
import tv.onscreen.mobile.data.prefs.SubtitleStyle.Size
import tv.onscreen.mobile.data.prefs.SubtitleStyle.TextColor

/**
 * Pure unit tests for [SubtitleStyle]. Mirror the web client's
 * `subtitle-style.test.ts` coverage — same defaults, same fail-soft
 * behaviour on unknown tokens, same per-token CSS-equivalent emit.
 *
 * Lives in `test/` (JVM unit-test sourceset) so it runs against plain
 * Kotlin without a device emulator.
 */
class SubtitleStyleTest {

    @Test
    fun `default style matches web defaults`() {
        // medium / white / translucent / light — same shape as
        // web DEFAULT_SUBTITLE_STYLE.
        val s = SubtitleStyle.DEFAULT
        assertThat(s.size).isEqualTo(Size.MEDIUM)
        assertThat(s.color).isEqualTo(TextColor.WHITE)
        assertThat(s.background).isEqualTo(Background.TRANSLUCENT)
        assertThat(s.outline).isEqualTo(Outline.LIGHT)
    }

    // ── parseSize ─────────────────────────────────────────────────────────

    @Test
    fun `parseSize accepts the three valid tokens`() {
        assertThat(SubtitleStyle.parseSize("small")).isEqualTo(Size.SMALL)
        assertThat(SubtitleStyle.parseSize("medium")).isEqualTo(Size.MEDIUM)
        assertThat(SubtitleStyle.parseSize("large")).isEqualTo(Size.LARGE)
    }

    @Test
    fun `parseSize falls back to MEDIUM on unknown or null`() {
        assertThat(SubtitleStyle.parseSize(null)).isEqualTo(Size.MEDIUM)
        assertThat(SubtitleStyle.parseSize("")).isEqualTo(Size.MEDIUM)
        assertThat(SubtitleStyle.parseSize("gigantic")).isEqualTo(Size.MEDIUM)
        assertThat(SubtitleStyle.parseSize("LARGE")).isEqualTo(Size.MEDIUM)  // case-sensitive
    }

    // ── parseColor ────────────────────────────────────────────────────────

    @Test
    fun `parseColor accepts each named colour`() {
        assertThat(SubtitleStyle.parseColor("white")).isEqualTo(TextColor.WHITE)
        assertThat(SubtitleStyle.parseColor("yellow")).isEqualTo(TextColor.YELLOW)
        assertThat(SubtitleStyle.parseColor("black")).isEqualTo(TextColor.BLACK)
        assertThat(SubtitleStyle.parseColor("red")).isEqualTo(TextColor.RED)
    }

    @Test
    fun `parseColor falls back to WHITE on unknown`() {
        assertThat(SubtitleStyle.parseColor(null)).isEqualTo(TextColor.WHITE)
        assertThat(SubtitleStyle.parseColor("magenta")).isEqualTo(TextColor.WHITE)
    }

    // ── parseBackground / parseOutline ────────────────────────────────────

    @Test
    fun `parseBackground accepts none, translucent, opaque`() {
        assertThat(SubtitleStyle.parseBackground("none")).isEqualTo(Background.NONE)
        assertThat(SubtitleStyle.parseBackground("translucent")).isEqualTo(Background.TRANSLUCENT)
        assertThat(SubtitleStyle.parseBackground("opaque")).isEqualTo(Background.OPAQUE)
        assertThat(SubtitleStyle.parseBackground("garbage")).isEqualTo(Background.TRANSLUCENT)
    }

    @Test
    fun `parseOutline accepts none, light, heavy`() {
        assertThat(SubtitleStyle.parseOutline("none")).isEqualTo(Outline.NONE)
        assertThat(SubtitleStyle.parseOutline("light")).isEqualTo(Outline.LIGHT)
        assertThat(SubtitleStyle.parseOutline("heavy")).isEqualTo(Outline.HEAVY)
        assertThat(SubtitleStyle.parseOutline("medium")).isEqualTo(Outline.LIGHT)
    }

    // ── round-trip serialise / parse ─────────────────────────────────────

    @Test
    fun `serialise then parse round-trips every enum value`() {
        for (v in Size.values()) {
            assertThat(SubtitleStyle.parseSize(SubtitleStyle.serializeSize(v))).isEqualTo(v)
        }
        for (v in TextColor.values()) {
            assertThat(SubtitleStyle.parseColor(SubtitleStyle.serializeColor(v))).isEqualTo(v)
        }
        for (v in Background.values()) {
            assertThat(SubtitleStyle.parseBackground(SubtitleStyle.serializeBackground(v))).isEqualTo(v)
        }
        for (v in Outline.values()) {
            assertThat(SubtitleStyle.parseOutline(SubtitleStyle.serializeOutline(v))).isEqualTo(v)
        }
    }

    // ── colour ↔ ARGB / size ↔ fractional / outline ↔ edge ──────────────

    @Test
    fun `foregroundColor returns expected ARGB ints`() {
        assertThat(SubtitleStyle.foregroundColor(TextColor.WHITE)).isEqualTo(0xFFFFFFFF.toInt())
        assertThat(SubtitleStyle.foregroundColor(TextColor.YELLOW)).isEqualTo(0xFFFFEB3B.toInt())
        assertThat(SubtitleStyle.foregroundColor(TextColor.BLACK)).isEqualTo(0xFF000000.toInt())
        assertThat(SubtitleStyle.foregroundColor(TextColor.RED)).isEqualTo(0xFFFF5252.toInt())
    }

    @Test
    fun `backgroundColor — none is fully transparent, opaque is solid`() {
        assertThat(SubtitleStyle.backgroundColor(Background.NONE)).isEqualTo(0x00000000)
        // Translucent should be ~0.75 alpha on black.
        val translucent = SubtitleStyle.backgroundColor(Background.TRANSLUCENT)
        val alpha = (translucent ushr 24) and 0xFF
        assertThat(alpha).isAtLeast(0xB0)
        assertThat(alpha).isAtMost(0xCF)
        assertThat(SubtitleStyle.backgroundColor(Background.OPAQUE)).isEqualTo(0xFF000000.toInt())
    }

    @Test
    fun `fractionalTextSize is monotonically increasing`() {
        // Whatever the exact values are, larger pickers must produce a
        // larger fractional text size — guards against a future tweak
        // that accidentally swaps two of the constants.
        val small = SubtitleStyle.fractionalTextSize(Size.SMALL)
        val medium = SubtitleStyle.fractionalTextSize(Size.MEDIUM)
        val large = SubtitleStyle.fractionalTextSize(Size.LARGE)
        assertThat(medium).isGreaterThan(small)
        assertThat(large).isGreaterThan(medium)
    }

    @Test
    fun `edgeType maps NONE - LIGHT - HEAVY onto distinct CaptionStyleCompat ints`() {
        // Three tokens → three distinct ints. Don't assert specific
        // values since they mirror Media3 constants we don't import in
        // this sourceset.
        val none = SubtitleStyle.edgeType(Outline.NONE)
        val light = SubtitleStyle.edgeType(Outline.LIGHT)
        val heavy = SubtitleStyle.edgeType(Outline.HEAVY)
        assertThat(setOf(none, light, heavy).size).isEqualTo(3)
        // NONE must equal CaptionStyleCompat.EDGE_TYPE_NONE = 0.
        assertThat(none).isEqualTo(0)
    }
}
