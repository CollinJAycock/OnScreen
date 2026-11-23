package tv.onscreen.mobile.ui.input

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowLeft
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import tv.onscreen.mobile.R

/**
 * Previous / next buttons at the sides of a full-screen pager (photo viewer,
 * comic reader), for a mouse or trackpad. They only show in a window at least
 * 600dp wide, where a tablet or Chromebook user may have no touch screen to
 * swipe with; a phone keeps the swipe-only viewer.
 *
 * The arrows are auto-mirrored and sit at Start / End, so in a right-to-left
 * layout (or a right-to-left comic) "previous" stays on the side the pager
 * goes back to.
 */
@Composable
fun BoxScope.PagerArrows(
    canGoBack: Boolean,
    canGoForward: Boolean,
    onBack: () -> Unit,
    onForward: () -> Unit,
) {
    if (LocalConfiguration.current.screenWidthDp < 600) return
    Box(Modifier.matchParentSize().safeDrawingPadding()) {
        if (canGoBack) {
            ArrowButton(
                onClick = onBack,
                label = stringResource(R.string.page_previous),
                forward = false,
                modifier = Modifier.align(Alignment.CenterStart),
            )
        }
        if (canGoForward) {
            ArrowButton(
                onClick = onForward,
                label = stringResource(R.string.page_next),
                forward = true,
                modifier = Modifier.align(Alignment.CenterEnd),
            )
        }
    }
}

@Composable
private fun ArrowButton(onClick: () -> Unit, label: String, forward: Boolean, modifier: Modifier) {
    IconButton(
        onClick = onClick,
        modifier = modifier
            .padding(12.dp)
            .background(Color.Black.copy(alpha = 0.45f), CircleShape),
    ) {
        Icon(
            if (forward) Icons.AutoMirrored.Filled.KeyboardArrowRight else Icons.AutoMirrored.Filled.KeyboardArrowLeft,
            contentDescription = label,
            tint = Color.White,
        )
    }
}
