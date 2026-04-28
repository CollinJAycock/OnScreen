package tv.onscreen.android.ui

import android.os.Bundle
import android.view.KeyEvent
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.prefs.ServerPrefs
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
