package tv.onscreen.android.ui

import android.app.PictureInPictureParams
import android.content.Intent
import android.content.res.Configuration
import android.os.Bundle
import android.os.Build
import android.util.Rational
import android.view.KeyEvent
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.playback.PlaybackFragment
import tv.onscreen.android.ui.setup.ServerSetupFragment
import tv.onscreen.android.ui.setup.LoginFragment
import tv.onscreen.android.ui.setup.PairingFragment
import tv.onscreen.android.ui.browse.HomeFragment
import javax.inject.Inject

/**
 * Implemented by fragments that need to receive global key events
 * regardless of where focus lands. Used by full-screen viewers
 * (PhotoViewFragment) where Leanback's focus search swallows
 * D-pad keys before they reach the fragment's OnKeyListener.
 *
 * Return true to consume the event; false to let it propagate
 * normally (so the fragment can forward back/escape to the
 * default handlers).
 */
interface KeyEventHandler {
    fun onActivityKeyEvent(event: KeyEvent): Boolean
}

@AndroidEntryPoint
class MainActivity : FragmentActivity() {

    @Inject
    lateinit var prefs: ServerPrefs

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        if (savedInstanceState != null) return // Fragment state restored by system.

        // Watch Next deep link first — when the system "Continue
        // Watching" tile launches us, jump straight into playback so
        // the user picks up where they left off without traversing
        // home → library → episode.
        if (handleWatchNextDeepLink(intent)) return

        lifecycleScope.launch {
            val hasServer = prefs.hasServer.first()
            val isLoggedIn = prefs.isLoggedIn.first()

            val fragment = when {
                !hasServer -> ServerSetupFragment()
                !isLoggedIn -> LoginFragment()
                else -> HomeFragment()
            }

            supportFragmentManager.beginTransaction()
                .replace(R.id.main_container, fragment)
                .commit()
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        // Re-launch from the launcher's Watch Next tile while the
        // activity is already alive — replace the current fragment
        // with PlaybackFragment for the requested item.
        handleWatchNextDeepLink(intent)
    }

    /**
     * If [intent] carries an `onscreen://watch/<item_id>?position=<ms>`
     * URI, route into PlaybackFragment for that item and return true.
     * Otherwise return false so the caller can fall through to the
     * normal startup path. Auth / server checks are skipped here —
     * if the user can't reach the server PlaybackFragment will
     * surface that in its own error overlay rather than us silently
     * dropping the deep link on the floor.
     */
    private fun handleWatchNextDeepLink(intent: Intent?): Boolean {
        val data = intent?.data ?: return false
        if (data.scheme != "onscreen" || data.host != "watch") return false
        val itemId = data.lastPathSegment ?: return false
        val position = data.getQueryParameter("position")?.toLongOrNull() ?: 0L
        supportFragmentManager.popBackStack(
            null,
            androidx.fragment.app.FragmentManager.POP_BACK_STACK_INCLUSIVE,
        )
        supportFragmentManager.beginTransaction()
            .replace(R.id.main_container, PlaybackFragment.newInstance(itemId, position))
            .commit()
        return true
    }

    /**
     * Activity-level key dispatch. Overrides the standard path so
     * full-screen fragments (PhotoViewFragment) that can't reliably
     * pull focus inside Leanback's container hierarchy still get a
     * shot at handling D-pad / media keys before the parent grid
     * consumes them. Fragments opt in by implementing [KeyEventHandler].
     *
     * Order: only ACTION_DOWN events go to the fragment; ACTION_UP
     * events flow through normally. Fragments that don't implement
     * the interface (the default — most fragments rely on focus +
     * OnKeyListener) see no behavioural change.
     */
    /** Hook the user-leave gesture (Home button on Android TV remote)
     *  and pop the active video into picture-in-picture instead of
     *  pausing it outright. Skipped for music — the audio backdrop
     *  doesn't need a floating window, and the upcoming media-session
     *  service keeps audio playing whether or not the activity is
     *  foreground. */
    override fun onUserLeaveHint() {
        super.onUserLeaveHint()
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val current = supportFragmentManager.findFragmentById(R.id.main_container)
        val pip = (current as? tv.onscreen.android.ui.playback.PlaybackFragment)?.activePiPAspect()
        if (pip != null) {
            val params = PictureInPictureParams.Builder()
                .setAspectRatio(Rational(pip.first, pip.second))
                .build()
            try { enterPictureInPictureMode(params) } catch (_: Exception) { }
        }
    }

    override fun onPictureInPictureModeChanged(isInPictureInPictureMode: Boolean, newConfig: Configuration) {
        super.onPictureInPictureModeChanged(isInPictureInPictureMode, newConfig)
        // Forward into the active fragment so it can hide the
        // Leanback transport controls in PiP (the chrome wouldn't
        // fit in a 240×135 window anyway).
        val current = supportFragmentManager.findFragmentById(R.id.main_container)
        (current as? tv.onscreen.android.ui.playback.PlaybackFragment)?.onPiPModeChanged(isInPictureInPictureMode)
    }

    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (event.action == KeyEvent.ACTION_DOWN) {
            val current = supportFragmentManager.findFragmentById(R.id.main_container)
            if (current is KeyEventHandler && current.onActivityKeyEvent(event)) {
                return true
            }
        }
        return super.dispatchKeyEvent(event)
    }

    /** Navigate to a destination, replacing the current fragment. */
    fun navigateTo(destination: NavigationDestination) {
        val fragment = when (destination) {
            NavigationDestination.SERVER_SETUP -> ServerSetupFragment()
            NavigationDestination.LOGIN -> LoginFragment()
            NavigationDestination.PAIRING -> PairingFragment()
            NavigationDestination.HOME -> HomeFragment()
        }

        // HOME is a terminal state — the user has finished
        // setup/login/pairing. Drop the entire back stack so the
        // setup screens don't linger (PairingFragment was sitting
        // in the stack and the user had to dismiss it manually
        // after sign-in completed) and Back from Home doesn't
        // drop the user back into the login flow.
        if (destination == NavigationDestination.HOME) {
            supportFragmentManager.popBackStack(
                null,
                androidx.fragment.app.FragmentManager.POP_BACK_STACK_INCLUSIVE,
            )
        }

        supportFragmentManager.beginTransaction()
            .replace(R.id.main_container, fragment)
            .apply {
                if (destination != NavigationDestination.HOME) {
                    addToBackStack(null)
                }
            }
            .commit()
    }
}

enum class NavigationDestination {
    SERVER_SETUP,
    LOGIN,
    PAIRING,
    HOME,
}
