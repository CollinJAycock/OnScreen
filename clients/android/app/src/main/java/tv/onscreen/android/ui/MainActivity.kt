package tv.onscreen.android.ui

import android.content.Intent
import android.os.Bundle
import android.util.Log
import android.view.KeyEvent
import androidx.fragment.app.FragmentActivity
import java.util.UUID
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.device.ClientName
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.CapabilitiesRepository
import tv.onscreen.android.data.repository.NotificationsRepository
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

    @Inject
    lateinit var clientName: ClientName

    @Inject
    lateinit var notifications: NotificationsRepository

    @Inject
    lateinit var capabilities: CapabilitiesRepository

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        // App-wide listeners that depend on auth — capabilities prefetch
        // and the cross-device playback.transfer handler. Both gate on
        // isLoggedIn so the SSE subscription doesn't try to dial the
        // server before the bearer is set; both run for the lifetime
        // of the activity (fragments come and go around them).
        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.STARTED) {
                if (!prefs.isLoggedIn.first()) return@repeatOnLifecycle
                // Capabilities prefetch — single-flight via the repo,
                // so a duplicate call from HomeFragment is a no-op.
                capabilities.getCachedOrFetch()
                listenForPlaybackTransfers()
            }
        }

        // Mid-session logout watch: TokenAuthenticator clears auth when the
        // refresh token is dead/reused, but only this onCreate routes by login
        // state — so the user would otherwise be stranded on a broken Home until
        // relaunch. Route back to login on a logged-in → logged-out transition.
        lifecycleScope.launch {
            var wasLoggedIn = prefs.isLoggedIn.first()
            prefs.isLoggedIn.collect { loggedIn ->
                if (wasLoggedIn && !loggedIn &&
                    prefs.hasServer.first() && !supportFragmentManager.isStateSaved
                ) {
                    navigateTo(NavigationDestination.LOGIN)
                }
                wasLoggedIn = loggedIn
            }
        }

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

            // Guard against the activity having already saved state
            // by the time the prefs flow emits — `am start` from a
            // launcher tile can race with the lifecycle so the
            // coroutine resumes after onSaveInstanceState, where a
            // plain commit() throws IllegalStateException. State-loss
            // is acceptable here: this is the initial-route decision,
            // and if the activity is recreated it will re-run this
            // exact branch from a fresh onCreate.
            if (supportFragmentManager.isStateSaved) return@launch
            supportFragmentManager.beginTransaction()
                .replace(R.id.main_container, fragment)
                .commitAllowingStateLoss()
        }
    }

    /**
     * Cross-device "play on this TV" handler. Subscribes to the
     * playback.transfer SSE channel and, when an event targets this
     * device's [ClientName], swaps the foreground fragment for a
     * PlaybackFragment loaded with the requested item + offset.
     *
     * The match is exact-string against `target_client_name` because
     * the SSE channel is per-user, not per-device — every subscribed
     * client of this user receives every transfer event. Without the
     * filter, hitting "Play on Living Room TV" from a phone would
     * also yank the Bedroom TV into playing the same item.
     *
     * The fragment swap reuses the same path the Watch Next deep
     * link uses (popBackStack + replace) so back-press from playback
     * lands on Home rather than walking up a stale stack.
     */
    private suspend fun listenForPlaybackTransfers() {
        // Reconnect loop with timeout-tolerance. The underlying SSE
        // socket times out (java.net.SocketTimeoutException) under load
        // — typically while a video is playing and the connection
        // starves enough to miss the server's keepalive. Without this
        // try/catch the timeout propagates up to lifecycleScope.launch
        // on the main dispatcher and crashes the activity, kicking the
        // user back to the Fire TV launcher mid-show while audio
        // continues briefly on its own thread until the process dies.
        // Same shape as PlaybackFragment.startCrossDeviceSync.
        while (currentCoroutineContext().isActive) {
            try {
                notifications.subscribePlaybackTransfers().collect { ev ->
                    if (ev.target_client_name != clientName.value) return@collect
                    if (supportFragmentManager.isStateSaved) return@collect
                    supportFragmentManager.popBackStack(
                        null,
                        androidx.fragment.app.FragmentManager.POP_BACK_STACK_INCLUSIVE,
                    )
                    supportFragmentManager.beginTransaction()
                        .replace(
                            R.id.main_container,
                            PlaybackFragment.newInstance(ev.item_id, ev.position_ms),
                        )
                        .commitAllowingStateLoss()
                }
            } catch (_: Exception) {
                // Stream dropped (timeout, server restart, network blip);
                // reconnect after a short delay.
            }
            delay(5_000)
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
        val raw = data.lastPathSegment ?: return false
        // UUID-validate before navigating. The deep link is callable by
        // any installed app via a crafted Intent; the server rejects
        // non-UUID ids with 400, but client-side validation stops a
        // garbage id from polluting our nav stack / firing a wasted
        // round-trip / leaving us on a broken playback screen the user
        // has to back out of.
        val itemId = try {
            UUID.fromString(raw).toString()
        } catch (_: IllegalArgumentException) {
            Log.w("MainActivity", "ignoring watch deep link with non-UUID id")
            return false
        }
        val position = data.getQueryParameter("position")?.toLongOrNull()?.coerceAtLeast(0L) ?: 0L
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
     *
     * `@SuppressLint("RestrictedApi")`: lint flags this because
     * `ComponentActivity.dispatchKeyEvent` carries
     * `@RestrictTo(LIBRARY_GROUP_PREFIX)` — but we're overriding the
     * public `Activity.dispatchKeyEvent` method (which has been part of
     * the platform Activity API since API 1). The `LIBRARY_GROUP_PREFIX`
     * restriction is about calls FROM non-androidx artifacts, not about
     * OVERRIDES from app code. Documented Android pattern; safe.
     */
    @android.annotation.SuppressLint("RestrictedApi")
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
