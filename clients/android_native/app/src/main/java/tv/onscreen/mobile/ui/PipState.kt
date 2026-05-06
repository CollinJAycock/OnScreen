package tv.onscreen.mobile.ui

import androidx.compose.runtime.compositionLocalOf

/**
 * Composition-local boolean: true while the host activity is in
 * Picture-in-Picture mode. PlayerScreen reads this to hide chrome
 * (toolbars, dialogs, overlays, the player's built-in controller)
 * so PiP renders only the video surface.
 *
 * MainActivity owns the underlying state and propagates it via
 * CompositionLocalProvider in setContent — see MainActivity for the
 * onPictureInPictureModeChanged hook that flips it.
 *
 * Defaults to false so the rest of the tree (which doesn't care
 * about PiP) renders normally without any wrapping provider.
 */
val LocalInPipMode = compositionLocalOf { false }
