package tv.onscreen.mobile.ui.player

import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.CaptionStyleCompat
import androidx.media3.ui.PlayerView
import androidx.media3.ui.SubtitleView
import tv.onscreen.mobile.data.prefs.SubtitleStyle

/**
 * Bridges [SubtitleStyle] to Media3's [SubtitleView] / [CaptionStyleCompat].
 *
 * Lives in `ui/player` (not `data/prefs`) so the data layer stays
 * Android-runtime-free for unit testing. The pure colour / size / edge
 * mappings are constants on [SubtitleStyle.Companion]; this file just
 * wraps them in the Media3 types.
 */
@UnstableApi
fun PlayerView.applySubtitleStyle(style: SubtitleStyle) {
    subtitleView?.applySubtitleStyle(style)
}

@UnstableApi
fun SubtitleView.applySubtitleStyle(style: SubtitleStyle) {
    setStyle(
        CaptionStyleCompat(
            SubtitleStyle.foregroundColor(style.color),
            SubtitleStyle.backgroundColor(style.background),
            // Window colour — the rectangle behind the entire subtitle
            // block. We use background per-cue (matches web), so the
            // window is transparent.
            0x00000000,
            SubtitleStyle.edgeType(style.outline),
            SubtitleStyle.EDGE_COLOR_BLACK,
            null, // typeface — null = system default; future hook for a font picker
        ),
    )
    // SubtitleView measures its parent height once at attach time; the
    // fractional value is re-applied on every style change so the user
    // immediately sees a size flip without rotating the device.
    setFractionalTextSize(SubtitleStyle.fractionalTextSize(style.size))
}
