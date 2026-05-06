package tv.onscreen.mobile

import android.app.PictureInPictureParams
import android.content.res.Configuration
import android.os.Build
import android.os.Bundle
import android.util.Rational
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import dagger.hilt.android.AndroidEntryPoint
import tv.onscreen.mobile.playback.ActiveVideoTracker
import tv.onscreen.mobile.ui.LocalInPipMode
import tv.onscreen.mobile.ui.nav.AppNav
import tv.onscreen.mobile.ui.theme.OnScreenTheme

@AndroidEntryPoint
class MainActivity : ComponentActivity() {

    // PiP state hoisted to Compose via LocalInPipMode. Updated only
    // from onPictureInPictureModeChanged so the source of truth stays
    // the system framework — any activity setting that flips us in/out
    // (config change, multi-window, etc.) re-fires this callback and
    // the Compose tree recomposes.
    private var inPipMode by mutableStateOf(false)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            OnScreenTheme {
                Surface(modifier = Modifier.fillMaxSize()) {
                    CompositionLocalProvider(LocalInPipMode provides inPipMode) {
                        AppNav()
                    }
                }
            }
        }
    }

    /**
     * Auto-enter PiP when the user navigates home or backgrounds
     * the app with a video playing. Without this, hitting home
     * during a movie pauses + collapses the player to nothing.
     *
     * Gated on ActiveVideoTracker so audio-only playback doesn't
     * trigger PiP — that path uses OnScreenMediaSessionService for
     * backgrounding instead.
     */
    override fun onUserLeaveHint() {
        super.onUserLeaveHint()
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        if (!ActiveVideoTracker.isPlaying()) return
        // Already in PiP (e.g. user dragged the floating window
        // around) — re-entering would be a no-op but the OS rejects
        // it noisily on some skins.
        if (isInPictureInPictureMode) return
        try {
            enterPictureInPictureMode(
                PictureInPictureParams.Builder()
                    .setAspectRatio(Rational(16, 9))
                    .build(),
            )
        } catch (_: Exception) {
            // Some launchers / form-factors reject PiP — swallow
            // rather than crash. The player keeps running in the
            // background; the user can resume by reopening the app.
        }
    }

    override fun onPictureInPictureModeChanged(
        isInPictureInPictureMode: Boolean,
        newConfig: Configuration,
    ) {
        super.onPictureInPictureModeChanged(isInPictureInPictureMode, newConfig)
        inPipMode = isInPictureInPictureMode
    }
}
