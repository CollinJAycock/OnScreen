package tv.onscreen.android.ui

import android.os.Bundle
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.ui.setup.ServerSetupFragment
import tv.onscreen.android.ui.setup.LoginFragment
import tv.onscreen.android.ui.browse.HomeFragment
import javax.inject.Inject

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

    /** Navigate to a destination, replacing the current fragment. */
    fun navigateTo(destination: NavigationDestination) {
        val fragment = when (destination) {
            NavigationDestination.SERVER_SETUP -> ServerSetupFragment()
            NavigationDestination.LOGIN -> LoginFragment()
            NavigationDestination.HOME -> HomeFragment()
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
    HOME,
}
