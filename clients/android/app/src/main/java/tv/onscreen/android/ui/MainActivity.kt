package tv.onscreen.android.ui

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.Bundle
import android.util.Log
import android.view.KeyEvent
import androidx.core.content.ContextCompat
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

    /** Registered at field-init time — [registerForActivityResult] must be
     *  called before the activity reaches STARTED or it throws. */
    private val notificationPermissionLauncher =
        registerForActivityResult(
            androidx.activity.result.contract.ActivityResultContracts.RequestPermission(),
        ) { /* denial is non-fatal — playback continues without the media rail */ }

    /** True when the next foreground entry should land on the Home screen
     *  instead of whatever screen was up when the app left. Set in onStop
     *  (HOME press, or a TV power-off on devices that do deliver lifecycle)
     *  and by [screenOffReceiver] when a fragment transaction isn't possible
     *  at that moment; consumed in onStart. The app's posture is
     *  home-on-every-start: leaving the foreground for any reason means the
     *  next entry starts fresh from Home. */
    private var pendingHomeReset = false

    /** Fire TV sticks don't reliably deliver onPause/onStop on an HDMI
     *  display-off — the activity can sit "resumed" on a dark panel with the
     *  old screen still live (a playback surface whose player is dead or
     *  still decoding), and that's exactly what the user sees when the TV
     *  comes back on. Catch SCREEN_OFF at the activity level and reset to
     *  Home right there: replacing the fragment runs its normal teardown, so
     *  video stops fully (final position reported, transcode session
     *  released) while music / audiobooks park to the MediaSessionService
     *  and keep playing — the same split HOME-backgrounding uses. SCREEN_ON
     *  then refreshes the Home rows, since on these devices no lifecycle
     *  callback fires on wake to trigger a reload. Registered
     *  onCreate→onDestroy precisely because onStop is the callback these
     *  devices fail to deliver. */
    private val screenOffReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            when (intent?.action) {
                Intent.ACTION_SCREEN_OFF -> resetToHome()
                Intent.ACTION_SCREEN_ON -> refreshHomeAfterWake()
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        ContextCompat.registerReceiver(
            this,
            screenOffReceiver,
            IntentFilter().apply {
                addAction(Intent.ACTION_SCREEN_OFF)
                addAction(Intent.ACTION_SCREEN_ON)
            },
            ContextCompat.RECEIVER_NOT_EXPORTED,
        )

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

        // Watch Next deep link first — when the system "Continue
        // Watching" tile launches us, jump straight into playback so
        // the user picks up where they left off without traversing
        // home → library → episode. Checked before the restored-state
        // branch so a deep-link launch that recreated the process still
        // lands on playback, not on a reset-to-Home.
        if (handleWatchNextDeepLink(intent)) return

        if (savedInstanceState != null) {
            // The system restored a previous fragment stack (process
            // death while backgrounded / TV power cycle). Home-on-every-
            // start: don't resume into whatever screen that was — defer
            // to the onStart reset, which drops the restored stack and
            // routes by current auth state.
            pendingHomeReset = true
            return
        }

        requestNotificationPermissionIfNeeded()

        lifecycleScope.launch {
            // Guard against the activity having already saved state
            // by the time the prefs flow emits — `am start` from a
            // launcher tile can race with the lifecycle so the
            // coroutine resumes after onSaveInstanceState, where a
            // plain commit() throws IllegalStateException. Deferring to
            // the onStart handler (rather than just dropping the route)
            // matters: abandoning it outright left main_container empty
            // with nothing to recover it, which stranded a first-run
            // user on a blank screen.
            if (supportFragmentManager.isStateSaved) {
                pendingHomeReset = true
                return@launch
            }
            routeToRoot()
        }
    }

    override fun onStart() {
        super.onStart()
        if (pendingHomeReset) {
            pendingHomeReset = false
            resetToHome()
        }
    }

    override fun onStop() {
        super.onStop()
        // Home-on-every-start: any exit from the foreground (HOME press,
        // launcher switch, TV power-off on devices that do deliver
        // lifecycle) means the next entry starts from the Home screen.
        pendingHomeReset = true
    }

    override fun onDestroy() {
        runCatching { unregisterReceiver(screenOffReceiver) }
        super.onDestroy()
    }

    /**
     * Route to the correct root screen for the current auth state, dropping
     * whatever back stack has built up. Skips the commit when that root is
     * already showing, and defers to the onStart consumer via
     * [pendingHomeReset] when instance state is saved (a SCREEN_OFF broadcast
     * racing a real onStop) rather than committing illegally.
     *
     * Routes by state rather than unconditionally to Home so a signed-out or
     * unconfigured user lands on Login / ServerSetup instead of a Home screen
     * that has no server to talk to. This is also the recovery path for an
     * initial route that had to be deferred.
     *
     * Replacing the current fragment runs its normal teardown: video playback
     * stops fully (final position reported, transcode session released),
     * audio parks to the MediaSessionService and keeps playing — re-entering
     * the track from Home reclaims the same player seamlessly via AudioHandoff.
     */
    private fun resetToHome() {
        lifecycleScope.launch {
            if (supportFragmentManager.isStateSaved) {
                pendingHomeReset = true
                return@launch
            }
            routeToRoot(popBackStack = true)
        }
    }

    /**
     * Commit the root fragment matching current auth state. Caller must have
     * already checked [androidx.fragment.app.FragmentManager.isStateSaved].
     */
    private suspend fun routeToRoot(popBackStack: Boolean = false) {
        val serverConfigured = prefs.hasServer.first()
        val signedIn = prefs.isLoggedIn.first()

        val target: androidx.fragment.app.Fragment = when {
            serverConfigured.not() -> ServerSetupFragment()
            signedIn.not() -> LoginFragment()
            else -> HomeFragment()
        }

        // Already on the right root — skip the commit so a SCREEN_OFF while
        // sitting on Home doesn't needlessly rebuild it. A null container
        // always needs a commit even though no type would match it.
        val current = supportFragmentManager.findFragmentById(R.id.main_container)
        if (current != null && current::class == target::class) return

        if (supportFragmentManager.isStateSaved) {
            pendingHomeReset = true
            return
        }
        if (popBackStack) {
            supportFragmentManager.popBackStack(
                null,
                androidx.fragment.app.FragmentManager.POP_BACK_STACK_INCLUSIVE,
            )
        }
        supportFragmentManager.beginTransaction()
            .replace(R.id.main_container, target)
            .commitAllowingStateLoss()
    }

    /**
     * API 33+ gates the media notification behind POST_NOTIFICATIONS. That
     * notification is the only transport control for audio the app
     * deliberately keeps playing after the player screen is gone, so without
     * the grant a user who backgrounds music has no way to pause it. Silent
     * no-op below API 33 and when already granted; a denial is non-fatal
     * (playback itself is unaffected).
     */
    private fun requestNotificationPermissionIfNeeded() {
        if (android.os.Build.VERSION.SDK_INT < 33) return
        val perm = android.Manifest.permission.POST_NOTIFICATIONS
        if (ContextCompat.checkSelfPermission(this, perm) ==
            android.content.pm.PackageManager.PERMISSION_GRANTED
        ) {
            return
        }
        runCatching { notificationPermissionLauncher.launch(perm) }
    }

    /**
     * SCREEN_ON companion to the SCREEN_OFF reset. On the devices that
     * skip lifecycle callbacks around a display power cycle, the Home
     * screen built at power-off time never gets an onResume on wake —
     * its rows would be hours stale (or stuck on a connection-error
     * overlay if the fetch raced the network going down). Swap in a
     * fresh HomeFragment so the rows reload now that the box is awake.
     * Only fires when Home is the visible fragment and state isn't
     * saved, so a normally-stopped background app is untouched (its
     * pending reset runs in onStart instead).
     */
    private fun refreshHomeAfterWake() {
        if (supportFragmentManager.isStateSaved) return
        if (supportFragmentManager.findFragmentById(R.id.main_container) !is HomeFragment) return
        supportFragmentManager.beginTransaction()
            .replace(R.id.main_container, HomeFragment())
            .commitAllowingStateLoss()
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
        // Keep getIntent() in step with what actually arrived, so the
        // consumed-marker below applies to the intent this activity record
        // will report from here on.
        setIntent(intent)
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
    private fun handleWatchNextDeepLink(launchIntent: Intent?): Boolean {
        val incoming = launchIntent ?: return false
        val data = incoming.data ?: return false
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
        // Consume the deep link so it fires exactly once. getIntent() is
        // sticky for the life of the ActivityRecord, so without clearing the
        // data every later recreation of an instance that was originally
        // launched from a Watch Next tile would re-handle the SAME stale VIEW
        // intent — re-entering playback at the tile's original position and
        // pre-empting both the restore branch and the auth routing below it.
        incoming.data = null
        setIntent(incoming)
        if (supportFragmentManager.isStateSaved) return false
        // A deep-link launch IS the user's chosen destination — don't let
        // a pending home reset (set when the app left the foreground)
        // clobber the playback screen we're about to show.
        pendingHomeReset = false
        supportFragmentManager.popBackStack(
            null,
            androidx.fragment.app.FragmentManager.POP_BACK_STACK_INCLUSIVE,
        )
        supportFragmentManager.beginTransaction()
            .replace(R.id.main_container, PlaybackFragment.newInstance(itemId, position))
            .commitAllowingStateLoss()
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
