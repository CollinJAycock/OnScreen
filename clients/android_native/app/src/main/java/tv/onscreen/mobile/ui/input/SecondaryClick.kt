package tv.onscreen.mobile.ui.input

import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.input.pointer.isSecondaryPressed
import androidx.compose.ui.input.pointer.pointerInput

/**
 * The mouse twin of a long-press: a right-click (secondary button, a
 * two-finger trackpad click on a Chromebook) runs [onSecondaryClick]. Touch,
 * pen and the primary button are untouched.
 *
 * Put it before the `combinedClickable` it pairs with: it sees the press on
 * the Initial pass and consumes it, so the right-click doesn't also count as
 * a tap. Null means no secondary action (the card has nothing to offer).
 */
fun Modifier.onSecondaryClick(onSecondaryClick: (() -> Unit)?): Modifier =
    if (onSecondaryClick == null) {
        this
    } else {
        composed {
            val action by rememberUpdatedState(onSecondaryClick)
            pointerInput(Unit) {
                awaitPointerEventScope {
                    while (true) {
                        val event = awaitPointerEvent(PointerEventPass.Initial)
                        if (event.type == PointerEventType.Press && event.buttons.isSecondaryPressed) {
                            event.changes.forEach { it.consume() }
                            action()
                        }
                    }
                }
            }
        }
    }
