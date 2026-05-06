package tv.onscreen.mobile.data.prefs

/**
 * Subtitle styling preferences for the in-player WebVTT renderer.
 *
 * Mirrors the web client's `subtitle-style.ts` shape so behaviour stays
 * consistent across surfaces: same tokens, same defaults, same fail-soft
 * posture on invalid values.
 *
 * This file deliberately has zero Android imports so the load / save /
 * normalisation logic can be unit-tested directly (no Robolectric, no
 * device emulator). The Compose-side translation to Media3's
 * CaptionStyleCompat lives in [tv.onscreen.mobile.ui.player.SubtitleStyleApplier].
 */
data class SubtitleStyle(
    val size: Size = Size.MEDIUM,
    val color: TextColor = TextColor.WHITE,
    val background: Background = Background.TRANSLUCENT,
    val outline: Outline = Outline.LIGHT,
) {
    enum class Size { SMALL, MEDIUM, LARGE }
    enum class TextColor { WHITE, YELLOW, BLACK, RED }
    enum class Background { NONE, TRANSLUCENT, OPAQUE }
    enum class Outline { NONE, LIGHT, HEAVY }

    companion object {
        val DEFAULT = SubtitleStyle()

        // Size → fractional text size for SubtitleView.setFractionalTextSize.
        // Numbers chosen to roughly match the web client's `font-size` rems
        // (1rem / 1.4rem / 2rem) when the player viewport is the full screen
        // — phone players are smaller than desktops, so the upper end leans
        // bigger so subtitles stay readable on a 6" handset.
        const val SIZE_SMALL: Float = 0.0533f   // ≈ stock SubtitleView default
        const val SIZE_MEDIUM: Float = 0.075f   // ≈ 40% bigger
        const val SIZE_LARGE: Float = 0.105f    // ≈ 100% bigger

        fun fractionalTextSize(s: Size): Float = when (s) {
            Size.SMALL -> SIZE_SMALL
            Size.MEDIUM -> SIZE_MEDIUM
            Size.LARGE -> SIZE_LARGE
        }

        // Color tokens → ARGB ints (alpha=0xFF for foreground).
        // Hexes match web cast.ts COLOR_HEX where applicable so a user
        // who picked yellow on the web sees the same hue on phone.
        const val COLOR_WHITE: Int = 0xFFFFFFFF.toInt()
        const val COLOR_YELLOW: Int = 0xFFFFEB3B.toInt()
        const val COLOR_BLACK: Int = 0xFF000000.toInt()
        const val COLOR_RED: Int = 0xFFFF5252.toInt()

        fun foregroundColor(c: TextColor): Int = when (c) {
            TextColor.WHITE -> COLOR_WHITE
            TextColor.YELLOW -> COLOR_YELLOW
            TextColor.BLACK -> COLOR_BLACK
            TextColor.RED -> COLOR_RED
        }

        // Background tokens → ARGB ints. `NONE` is fully transparent so
        // only the outline carries the cue. `TRANSLUCENT` is the v1
        // default — matches the web .subtitle-cue baseline. `OPAQUE` is
        // accessibility / high-contrast for users who can't read against
        // a translucent fill.
        const val BG_NONE: Int = 0x00000000
        const val BG_TRANSLUCENT: Int = 0xBF000000.toInt()  // ~0.75 alpha
        const val BG_OPAQUE: Int = 0xFF000000.toInt()

        fun backgroundColor(b: Background): Int = when (b) {
            Background.NONE -> BG_NONE
            Background.TRANSLUCENT -> BG_TRANSLUCENT
            Background.OPAQUE -> BG_OPAQUE
        }

        // Outline → edge-type + edge-color tuple. Media3's
        // CaptionStyleCompat carries one EDGE_TYPE field; we map our
        // tokens onto its EDGE_TYPE_OUTLINE / EDGE_TYPE_DROP_SHADOW
        // forms. Heavy uses OUTLINE for a thick black ring, light uses
        // DROP_SHADOW for a softer 1-2 px shadow, none clears it.
        //
        // Edge-type ints intentionally match
        // CaptionStyleCompat.EDGE_TYPE_* but live as plain Ints here so
        // the data layer doesn't import Media3.
        const val EDGE_TYPE_NONE: Int = 0
        const val EDGE_TYPE_OUTLINE: Int = 2
        const val EDGE_TYPE_DROP_SHADOW: Int = 3
        const val EDGE_COLOR_BLACK: Int = 0xFF000000.toInt()

        fun edgeType(o: Outline): Int = when (o) {
            Outline.NONE -> EDGE_TYPE_NONE
            Outline.LIGHT -> EDGE_TYPE_DROP_SHADOW
            Outline.HEAVY -> EDGE_TYPE_OUTLINE
        }

        // Tolerant token parsers — accept whatever's stored in DataStore
        // and fall back to defaults on unknown strings. Matches the web
        // helper's "never throw, never reset to default unless we have
        // to" posture.
        fun parseSize(s: String?): Size = when (s) {
            "small" -> Size.SMALL
            "large" -> Size.LARGE
            else -> Size.MEDIUM
        }
        fun parseColor(s: String?): TextColor = when (s) {
            "yellow" -> TextColor.YELLOW
            "black" -> TextColor.BLACK
            "red" -> TextColor.RED
            else -> TextColor.WHITE
        }
        fun parseBackground(s: String?): Background = when (s) {
            "none" -> Background.NONE
            "opaque" -> Background.OPAQUE
            else -> Background.TRANSLUCENT
        }
        fun parseOutline(s: String?): Outline = when (s) {
            "none" -> Outline.NONE
            "heavy" -> Outline.HEAVY
            else -> Outline.LIGHT
        }

        // Reverse — for serialisation back into DataStore. Lowercase
        // strings to match the web client's storage shape so a future
        // sync-prefs-across-devices feature can interop.
        fun serializeSize(s: Size): String = s.name.lowercase()
        fun serializeColor(c: TextColor): String = c.name.lowercase()
        fun serializeBackground(b: Background): String = b.name.lowercase()
        fun serializeOutline(o: Outline): String = o.name.lowercase()
    }
}
