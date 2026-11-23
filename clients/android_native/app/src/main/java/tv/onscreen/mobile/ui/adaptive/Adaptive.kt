package tv.onscreen.mobile.ui.adaptive

import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

/*
 * Large-screen layout (tablets, Chromebooks, desktop windowing), in one place.
 *
 * Everything keys off the WINDOW, not the display: Configuration.screenWidthDp
 * / screenHeightDp are the activity window's size, so split screen and a
 * resized ChromeOS window count. A phone, and a phone on its side (about
 * 400dp tall), stays on the phone layout: the thresholds below need some
 * height too.
 */

/** The window's size in dp, and what the app does with it. */
@Immutable
data class WindowLayout(val widthDp: Int, val heightDp: Int) {
    /** A navigation rail beside the content (AppNav). */
    val navigationRail: Boolean get() = widthDp >= 600 && heightDp >= 480

    /** Room for a list and its detail side by side (Settings, a library). */
    val twoPane: Boolean get() = widthDp >= 840 && heightDp >= 480
}

@Composable
fun rememberWindowLayout(): WindowLayout {
    val config = LocalConfiguration.current
    return remember(config.screenWidthDp, config.screenHeightDp) {
        WindowLayout(config.screenWidthDp, config.screenHeightDp)
    }
}

/** The widest a column of text, settings or list rows should get. */
val ReadableMaxWidth = 840.dp

/**
 * The width the current screen actually has: the window minus the navigation
 * rail, or one pane of a list-detail. Provided by [ProvideContentWidth];
 * unspecified outside one, which [readablePadding] treats as narrow.
 */
val LocalContentWidth = compositionLocalOf { Dp.Unspecified }

/** Measures the space it is given and provides it as [LocalContentWidth]. */
@Composable
fun ProvideContentWidth(modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    BoxWithConstraints(modifier) {
        CompositionLocalProvider(LocalContentWidth provides maxWidth) { content() }
    }
}

/** Extra side margin that centres a [ReadableMaxWidth] column in the content width. Zero on phones. */
@Composable
fun readableSideMargin(): Dp {
    val width = LocalContentWidth.current
    if (width == Dp.Unspecified || width <= ReadableMaxWidth) return 0.dp
    return (width - ReadableMaxWidth) / 2
}

/**
 * contentPadding for a full-width LazyColumn: [horizontal] / [vertical] as
 * before, plus [readableSideMargin] on each side. The list itself still spans
 * the width, so wheel and trackpad scrolling work over the margins.
 */
@Composable
fun readablePadding(horizontal: Dp = 16.dp, vertical: Dp = horizontal): PaddingValues {
    val side = readableSideMargin()
    return PaddingValues(horizontal = horizontal + side, vertical = vertical)
}

/** Minimum poster width for the adaptive grids: a few more, larger posters on wide windows. */
@Composable
fun posterMinWidth(): Dp {
    val width = LocalContentWidth.current
    return if (width != Dp.Unspecified && width >= ReadableMaxWidth) 150.dp else 120.dp
}
