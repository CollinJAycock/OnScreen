package tv.onscreen.mobile

import android.Manifest
import android.app.PictureInPictureParams
import android.content.Context
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.os.Build
import android.content.Intent
import android.os.Bundle
import android.util.Rational
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import javax.inject.Inject
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.AuthRepository
import tv.onscreen.mobile.i18n.AppLocales
import tv.onscreen.mobile.playback.ActiveVideoTracker
import tv.onscreen.mobile.ui.LocalInPipMode
import tv.onscreen.mobile.ui.nav.AppNav
import tv.onscreen.mobile.ui.theme.OnScreenTheme

// FragmentActivity (still a ComponentActivity, so setContent works): the
// Cast MediaRouteButton shows its device picker as a DialogFragment and
// throws "The activity must be a subclass of FragmentActivity" otherwise.
@AndroidEntryPoint
class MainActivity : FragmentActivity() {

    /** Self-heals a stale cleartext origin once per launch — see
     *  [AuthRepository.healStaleOrigin]. Only the sign-in paths probed
     *  before, which a signed-in session never revisits. */
    @Inject lateinit var authRepo: AuthRepository

    @Inject lateinit var prefs: ServerPrefs

    // PiP state hoisted to Compose via LocalInPipMode. Updated only
    // from onPictureInPictureModeChanged so the source of truth stays
    // the system framework — any activity setting that flips us in/out
    // (config change, multi-window, etc.) re-fires this callback and
    // the Compose tree recomposes.
    private var inPipMode by mutableStateOf(false)

    /** Android 13+ requires a runtime grant for POST_NOTIFICATIONS;
     *  without it the system silently suppresses every notification we
     *  post — including the foreground-service notification the
     *  download worker uses. The OS has been observed to kill the
     *  foreground service after a while when no notification is
     *  visible, so this isn't purely cosmetic. */
    private val requestNotifications =
        registerForActivityResult(ActivityResultContracts.RequestPermission()) { /* ignored */ }

    /** The app's language (AppLocales): on Android 12 and older this
     *  activity runs in a configuration context with the chosen locale;
     *  Android 13+ applies the per-app language itself. */
    override fun attachBaseContext(newBase: Context) {
        super.attachBaseContext(AppLocales.wrap(newBase))
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Android 12 and older: a language change (the Settings picker, or the
        // account's choice arriving with the preferences) recreates the
        // activity so every screen redraws in it. Android 13+ recreates it
        // itself when the per-app language changes.
        if (!AppLocales.platformManaged) {
            val shown = AppLocales.storedChoice.value
            lifecycleScope.launch {
                repeatOnLifecycle(Lifecycle.State.STARTED) {
                    AppLocales.storedChoice.collect { if (it != shown) recreate() }
                }
            }
        }
        // Draw behind the system bars. Forced-dark scrims keep the status/nav
        // icons light (the app is dark-only). Scaffold screens pick up the
        // resulting insets via their innerPadding; full-bleed screens (player,
        // photo, book) intentionally draw under the bars; the setup/pair screen
        // applies systemBarsPadding() itself.
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(android.graphics.Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.dark(android.graphics.Color.TRANSPARENT),
        )
        // The quest build doesn't declare it (see its manifest).
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !BuildConfig.IS_QUEST) {
            val granted = ContextCompat.checkSelfPermission(
                this, Manifest.permission.POST_NOTIFICATIONS,
            ) == PackageManager.PERMISSION_GRANTED
            if (!granted) requestNotifications.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
        // Wait for a signed-in state rather than sampling once: on a cold
        // start this activity is alive through pairing, so a single read
        // would see false and never run again. Once signed in, upgrade a
        // stale http origin if the server has since moved behind TLS —
        // otherwise every request 401s (GET) or 405s (POST) for the life
        // of the install, with no self-heal and no visible cause.
        lifecycleScope.launch {
            prefs.isLoggedIn.first { it }
            authRepo.healStaleOrigin()
        }
        if (savedInstanceState == null) playFromSearch(intent)
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

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        playFromSearch(intent)
    }

    /** "Play … on OnScreen" from the Assistant: hand the query to the
     *  background player once signed in (VoicePlay). */
    private fun playFromSearch(intent: Intent?) {
        val query = tv.onscreen.mobile.playback.VoicePlay.query(intent) ?: return
        lifecycleScope.launch {
            if (prefs.isLoggedIn.first()) tv.onscreen.mobile.playback.VoicePlay.play(this@MainActivity, query)
        }
    }

    /** Android 12+ supports declarative auto-enter PiP — set
     *  setAutoEnterEnabled(true) on params and the system handles
     *  the home-button gesture without an onUserLeaveHint() bridge.
     *  Gated on ActiveVideoTracker so we only register the params
     *  while a video is playing (otherwise the system would PiP an
     *  empty surface during routine navigation). Audio-only items
     *  intentionally aren't backgrounded — closing the player ends
     *  playback for tracks, audiobooks, and podcasts.
     *
     *  setSourceRectHint is supplied when the PlayerScreen has
     *  reported its surface bounds via ActiveVideoTracker — without
     *  it the OS falls back to a centred-zoom animation that doesn't
     *  morph out of the player view, so the auto-enter looks janky
     *  next to first-party apps. */
    private fun updatePipParams() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) return
        // A Quest panel has no picture-in-picture (the quest manifest turns
        // it off too).
        if (BuildConfig.IS_QUEST) return
        val playing = ActiveVideoTracker.isPlaying()
        try {
            val builder = PictureInPictureParams.Builder()
                .setAspectRatio(Rational(16, 9))
                .setAutoEnterEnabled(playing)
            ActiveVideoTracker.getSourceRect()?.let { builder.setSourceRectHint(it) }
            setPictureInPictureParams(builder.build())
        } catch (_: Exception) {
            // Some skins reject PiP outright — swallow.
        }
    }

    override fun onResume() {
        super.onResume()
        updatePipParams()
        // Re-fire setPictureInPictureParams whenever the player flips
        // the active-video flag so setAutoEnterEnabled tracks reality.
        ActiveVideoTracker.setListener { runOnUiThread { updatePipParams() } }
    }

    override fun onPause() {
        ActiveVideoTracker.setListener(null)
        super.onPause()
    }

    override fun onUserLeaveHint() {
        super.onUserLeaveHint()
        // Android 12+ auto-enters via setAutoEnterEnabled — onUserLeaveHint
        // would double-fire. Pre-12 still needs the manual call.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) return
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        if (BuildConfig.IS_QUEST) return
        if (!ActiveVideoTracker.isPlaying()) return
        if (isInPictureInPictureMode) return
        try {
            enterPictureInPictureMode(
                PictureInPictureParams.Builder()
                    .setAspectRatio(Rational(16, 9))
                    .build(),
            )
        } catch (_: Exception) {
            // Some launchers / form-factors reject PiP — swallow
            // rather than crash.
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
