package tv.onscreen.mobile.ui.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable

private val DarkColors = darkColorScheme(
    primary = OnScreenAccent,
    onPrimary = OnScreenOnSurfaceDark,
    primaryContainer = OnScreenAccentDark,
    onPrimaryContainer = OnScreenOnSurfaceDark,
    background = OnScreenSurfaceDark,
    onBackground = OnScreenOnSurfaceDark,
    surface = OnScreenSurfaceDark,
    onSurface = OnScreenOnSurfaceDark,
    surfaceVariant = OnScreenSurfaceVariantDark,
    onSurfaceVariant = OnScreenOnSurfaceMutedDark,
)

// OnScreen is intentionally a dark-only product — the palette in Color.kt is
// dark-tuned and the whole UI (overlays, scrims, badges) is designed against
// it. We therefore force the dark scheme regardless of the system setting; a
// half-built `lightColorScheme` (only primary defined) previously left a
// light-mode device rendering Material-default white surfaces under
// dark-designed copy. If a real light theme is ever wanted, build a complete
// lightColorScheme and gate on isSystemInDarkTheme() here.
@Composable
fun OnScreenTheme(
    content: @Composable () -> Unit,
) {
    MaterialTheme(
        colorScheme = DarkColors,
        content = content,
    )
}
