package tv.onscreen.mobile.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
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

private val LightColors = lightColorScheme(
    primary = OnScreenAccent,
    primaryContainer = OnScreenAccentDark,
)

@Composable
fun OnScreenTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    MaterialTheme(
        colorScheme = if (darkTheme) DarkColors else LightColors,
        content = content,
    )
}
