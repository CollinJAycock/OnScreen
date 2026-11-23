package tv.onscreen.mobile.ui.adaptive

import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.VerticalDivider
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier

/**
 * A list and, when [showDetail], its detail beside it: Settings and its
 * sub-screens, a library grid and the title picked in it, on a window wide
 * enough for both ([WindowLayout.twoPane]). Each pane provides its own
 * [LocalContentWidth], so screens inside lay out for the pane, not the window.
 *
 * With no detail showing, the list keeps the whole width. Callers open the
 * detail as a full-screen route instead when the window is narrow, and move an
 * open detail to its route if the window narrows.
 */
@Composable
fun ListDetail(
    showDetail: Boolean,
    list: @Composable () -> Unit,
    detail: @Composable () -> Unit,
    listWeight: Float = 0.45f,
) {
    Row(Modifier.fillMaxSize()) {
        ProvideContentWidth(Modifier.weight(if (showDetail) listWeight else 1f).fillMaxHeight()) { list() }
        if (showDetail) {
            VerticalDivider()
            ProvideContentWidth(Modifier.weight(1f - listWeight).fillMaxHeight()) { detail() }
        }
    }
}
