package tv.onscreen.mobile.ui.input

import android.view.InputDevice
import android.view.KeyEvent
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState

/**
 * Hardware-keyboard shortcuts for whichever screen is on top: the player,
 * the photo viewer and the readers, on Chromebooks and tablets with a
 * keyboard.
 *
 * Screens register a handler while they are composed ([KeyShortcutHandler]).
 * [tv.onscreen.mobile.MainActivity.dispatchKeyEvent] offers each key-down to
 * the newest handler before normal dispatch. Doing it at the Activity, not
 * with Compose `onKeyEvent`, means it works no matter where focus is:
 * nothing on these screens requests focus, and the player's PlayerView (an
 * AndroidView) would otherwise swallow arrow keys for its own controller.
 *
 * Only keys from a physical alphabetic keyboard reach a handler ([fromKeyboard]).
 * A Quest controller's thumbstick, a TV remote's D-pad and an on-screen
 * keyboard keep their normal behaviour, and media keys are never claimed
 * (MediaSession handles them).
 *
 * Main thread only, like all key dispatch.
 */
object KeyShortcuts {
    private val handlers = ArrayList<(KeyEvent) -> Boolean>()

    /** True when the top handler consumed [event]. */
    fun dispatch(event: KeyEvent): Boolean {
        if (event.action != KeyEvent.ACTION_DOWN || !fromKeyboard(event)) return false
        val top = handlers.lastOrNull() ?: return false
        return top(event)
    }

    internal fun push(handler: (KeyEvent) -> Boolean) {
        handlers.add(handler)
    }

    internal fun remove(handler: (KeyEvent) -> Boolean) {
        handlers.remove(handler)
    }

    /** A key from a full physical keyboard (not a D-pad, gamepad or IME). */
    fun fromKeyboard(event: KeyEvent): Boolean {
        val device = event.device ?: return false
        if (device.isVirtual) return false
        return (event.source and InputDevice.SOURCE_KEYBOARD) == InputDevice.SOURCE_KEYBOARD &&
            device.keyboardType == InputDevice.KEYBOARD_TYPE_ALPHABETIC
    }
}

/**
 * Registers [onKey] as the top keyboard-shortcut handler while this is in the
 * composition. Return true for keys it handled. Only key-downs from a physical
 * keyboard arrive; check [KeyEvent.getRepeatCount] for toggles.
 */
@Composable
fun KeyShortcutHandler(onKey: (KeyEvent) -> Boolean) {
    val current by rememberUpdatedState(onKey)
    DisposableEffect(Unit) {
        val handler: (KeyEvent) -> Boolean = { current(it) }
        KeyShortcuts.push(handler)
        onDispose { KeyShortcuts.remove(handler) }
    }
}
