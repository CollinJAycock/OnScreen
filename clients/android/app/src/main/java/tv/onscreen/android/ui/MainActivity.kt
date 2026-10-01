package tv.onscreen.android.ui

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.Bundle
import android.os.SystemClock
import android.util.Log
import android.view.KeyEvent
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import java.util.UUID
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.device.ClientName
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.AuthRepository
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

    @Inject
    lateinit var authRepo: AuthRepository

    /** Registered at field-init time — [registerForActivityResult] must be
     *  called before the activity reaches STARTED or it throws. */
    private val notificationPermissionLauncher =
        registerForActivityResult(
            androidx.activity.result.contract.ActivityResultContracts.RequestPermission(),
        ) { /* denial is non-fatal — playback continues without the media rail */ }

    /** True when the next foreground entry should land on the Home screen
     *  instead of whatever screen was up when the app left. Set by
     *  [screenOffReceiver] when a fragment transaction isn't possible at that
     *  moment, and for a first route that had to wait; consumed in onStart.
     *  onStop asks for the same through [stoppedAtMs]. The app's posture is
     *  home-on-every-start: leaving the foreground for any reason means the
     *  next entry starts fresh from Home — save the screensaver, which gives
     *  back the screen it covered (see [ScreensaverStop]). */
    private var pendingHomeReset = false

    /** When the activity last stopped (HOME press, or a TV power-off on
     *  devices that do deliver lifecycle), as elapsedRealtime, while the Home
     *  reset that stop asks for is still to come; null when none is. Kept
     *  apart from [pendingHomeReset] because onStart may waive it: a stop the
     *  screensaver caused is only known to be one once the dream's broadcast
     *  has arrived, which can be after onStop. A screen-off during the
     *  screensaver (the TV going to standby from it) still resets, through
     *  [pendingHomeReset]. */
    private var stoppedAtMs: Long? = null

    /** The last screensaver start and end [dreamReceiver] heard, as
     *  elapsedRealtime; null until one is. */
    private var dreamStartedAtMs: Long? = null
    private var dreamStoppedAtMs: Long? = null

    /** A Watch Next deep link that arrived while fragment state was saved
     *  (itemId to positionMs), for onStart to deliver. A guard only:
     *  FragmentActivity clears the saved-state flag as a new intent comes in,
     *  so a link from a tile clicked while the app is backgrounded commits
     *  at once from onNewIntent, on whichever side of onStart that lands.
     *  Before onStart, [showPlayback] clearing the pending reset
     *  ([pendingHomeReset], [stoppedAtMs]) keeps it; after onStart (API 30
     *  Fire TV), cancelling [homeResetJob] does. */
    private var pendingDeepLink: Pair<String, Long>? = null

    /** The Home reset in flight ([resetToHome]), or the wake from the
     *  screensaver ([keepScreenAfterScreensaver]). It reads the auth prefs
     *  before it commits, so for a moment it is suspended with Home still to
     *  come. A player committed in that moment (a Watch Next link delivered
     *  after onStart, a transfer from another device) was replaced by Home
     *  when the reset went on: [showPlayback] cancels it. */
    private var homeResetJob: Job? = null

    /** Display refresh-rate switching for video playback. Held here, not by
     *  the player screen: the window's display mode outlives any one player
     *  (see FrameRateSwitcher). */
    val frameRates: tv.onscreen.android.ui.playback.FrameRateSwitcher by lazy {
        tv.onscreen.android.ui.playback.FrameRateSwitcher(this)
    }

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

    /** Screensaver (dream) start and end, for onStart to tell a stop the
     *  screensaver caused from any other. Music left on the now-playing
     *  screen lets the screensaver come on (only video holds the screen on);
     *  the music plays on in the background service, as after HOME, and
     *  waking belongs back on the now-playing screen, not on Home. A dream
     *  keeps the device interactive, so no SCREEN_OFF comes with it.
     *  Registered onCreate→onDestroy like [screenOffReceiver]: it has to
     *  hear the dream while the activity is stopped. */
    private val dreamReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            when (intent?.action) {
                Intent.ACTION_DREAMING_STARTED -> dreamStartedAtMs = SystemClock.elapsedRealtime()
                Intent.ACTION_DREAMING_STOPPED -> dreamStoppedAtMs = SystemClock.elapsedRealtime()
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
        ContextCompat.registerReceiver(
            this,
            dreamReceiver,
            IntentFilter().apply {
                addAction(Intent.ACTION_DREAMING_STARTED)
                addAction(Intent.ACTION_DREAMING_STOPPED)
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
                // WAIT for a signed-in state; don't sample it once.
                //
                // This used to `return@repeatOnLifecycle` when not yet logged
                // in, which only re-runs on the next STOPPED -> STARTED
                // transition. On first run the activity never stops — setup,
                // login and Home are all fragment swaps inside it — so the
                // block sampled `false` at launch and never ran again. Neither
                // the capabilities prefetch nor cross-device "play to this
                // device" came up until the user happened to background and
                // foreground the app.
                prefs.isLoggedIn.first { it }
                // Self-heal a stale cleartext origin for an ALREADY-signed-in
                // install. checkServer adopts the answered origin at setup and
                // login/pairing probe before their POSTs, but a session that
                // predates those fixes — or a server that moved behind TLS
                // after setup — never re-runs either path, so every mutating
                // call (progress PUTs above all) silently 405s on the 301's
                // POST→GET rewrite, and the bearer transits the first hop in
                // the clear. Cheap: a no-op once the origin is https.
                authRepo.healStaleOrigin()
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
        // The device-level teardown for that same edge (Watch Next rows,
        // parked audio, identity caches) lives in SignOutTeardown, started
        // from OnScreenApp so it also runs when no activity is alive.
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
        // A deep link stashed by handleWatchNextDeepLink's saved-state guard
        // (not expected: a new intent clears that flag) wins over the home
        // reset — the tile IS the user's chosen destination.
        pendingDeepLink?.let { (itemId, position) ->
            pendingDeepLink = null
            showPlayback(itemId, position)
            return
        }
        // The last stop's Home reset, unless that stop was the screensaver
        // coming on and this start is the wake from it: then the screen it
        // covered is the one to come back to (the now-playing screen has
        // already taken its player back from the background service, in its
        // own onStart).
        val stoppedAt = stoppedAtMs
        stoppedAtMs = null
        val backFromScreensaver = stoppedAt != null && ScreensaverStop.returnsToScreen(
            stoppedAtMs = stoppedAt,
            dreamStartedAtMs = dreamStartedAtMs,
            dreamStoppedAtMs = dreamStoppedAtMs,
            nowMs = SystemClock.elapsedRealtime(),
        )
        if (pendingHomeReset || (stoppedAt != null && !backFromScreensaver)) {
            pendingHomeReset = false
            resetToHome()
        } else if (backFromScreensaver) {
            Log.i("MainActivity", "back from the screensaver: keeping the screen it covered")
            keepScreenAfterScreensaver()
        }
    }

    override fun onStop() {
        super.onStop()
        // Home-on-every-start: any exit from the foreground (HOME press,
        // launcher switch, TV power-off on devices that do deliver
        // lifecycle) means the next entry starts from the Home screen.
        // onStart makes the call: this stop may yet turn out to be the
        // screensaver's (see stoppedAtMs).
        stoppedAtMs = SystemClock.elapsedRealtime()
    }

    override fun onDestroy() {
        runCatching { unregisterReceiver(screenOffReceiver) }
        runCatching { unregisterReceiver(dreamReceiver) }
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
        homeResetJob?.cancel()
        homeResetJob = lifecycleScope.launch {
            if (supportFragmentManager.isStateSaved) {
                pendingHomeReset = true
                return@launch
            }
            routeToRoot(popBackStack = true)
        }
    }

    /**
     * The wake from the screensaver: the screen it covered stays up, and a
     * video on it reopens where it stopped (see
     * [PlaybackFragment.resumeAfterScreensaver]). The Home reset still
     * happens when there is nothing to come back to (music the background
     * service let go meanwhile), and when the app is no longer signed in: a
     * sign-out while the screensaver ran (a session revoked elsewhere) found
     * the activity stopped and couldn't route to Login then, and the onStart
     * reset that used to catch it up is the one skipped here. Likewise a
     * sign-in the screensaver covered that finished meanwhile (the pairing
     * code entered on a phone): its move to Home was dropped with the
     * activity stopped, and waking on the finished sign-in screen left the
     * user there. Held as [homeResetJob] so a player [showPlayback] commits
     * meanwhile wins.
     */
    private fun keepScreenAfterScreensaver() {
        homeResetJob?.cancel()
        homeResetJob = lifecycleScope.launch {
            val signedIn = prefs.hasServer.first() && prefs.isLoggedIn.first()
            currentCoroutineContext().ensureActive()
            val current = supportFragmentManager.findFragmentById(R.id.main_container)
            val onSignIn = current is LoginFragment || current is PairingFragment || current is ServerSetupFragment
            val playback = current as? PlaybackFragment
            if (!signedIn || onSignIn || playback?.resumeAfterScreensaver() == false) {
                routeToRoot(popBackStack = true)
            }
        }
    }

    /**
     * Swap in playback of [itemId] from [positionMs] as the only screen (the
     * back stack is dropped, so BACK from it leaves the app): the screen the
     * user just asked for (a Watch Next tile, "play on this TV" from another
     * device). A Home reset that is pending or already on its way must not
     * replace it. The players it moves on to (the next episode or track)
     * stay the only screen, and its end leaves the app (PlayerStack).
     */
    private fun showPlayback(itemId: String, positionMs: Long) {
        pendingHomeReset = false
        stoppedAtMs = null
        homeResetJob?.cancel()
        homeResetJob = null
        supportFragmentManager.popBackStack(
            null,
            androidx.fragment.app.FragmentManager.POP_BACK_STACK_INCLUSIVE,
        )
        supportFragmentManager.beginTransaction()
            .replace(R.id.main_container, PlaybackFragment.newInstance(itemId, positionMs))
            .commitAllowingStateLoss()
    }

    /**
     * Commit the root fragment matching current auth state. Caller must have
     * already checked [androidx.fragment.app.FragmentManager.isStateSaved].
     */
    private suspend fun routeToRoot(popBackStack: Boolean = false) {
        val serverConfigured = prefs.hasServer.first()
        val signedIn = prefs.isLoggedIn.first()
        // Cancelled while those reads were suspended ([showPlayback]):
        // another screen was chosen meanwhile, so no route at all.
        currentCoroutineContext().ensureActive()

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
        // Pairing is the other way to sign in, its code still on screen and
        // polled for: swapping it for the login screen (HOME and back, the
        // TV's power or screensaver) threw the code away while the user was
        // typing it on their phone.
        if (current is PairingFragment && target is LoginFragment) return

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
     * link uses ([showPlayback]), so back-press from playback leaves the
     * app rather than walking up a stale stack.
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
                    showPlayback(ev.item_id, ev.position_ms)
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
        // Belt-and-braces with the manifest (the filter is not BROWSABLE):
        // browsers stamp CATEGORY_BROWSABLE on anything a web page launches,
        // including Chrome-style intent: URIs naming this component. Launcher
        // Watch Next tiles never carry it, so refuse web-originated auto-play.
        if (incoming.hasCategory(Intent.CATEGORY_BROWSABLE)) {
            Log.w("MainActivity", "ignoring browsable watch deep link")
            return false
        }
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
        if (supportFragmentManager.isStateSaved) {
            // Not expected: super.onNewIntent clears the saved-state flag
            // (FragmentActivity's new-intent listener), and onCreate runs
            // before any save. But a commit now would throw, and the URI is
            // already consumed above (it must fire once), so stash it for
            // onStart rather than drop the user's chosen title.
            pendingDeepLink = itemId to position
            return true
        }
        // A deep-link launch IS the user's chosen destination — don't let
        // a home reset clobber the playback screen we're about to show:
        // pending (set when the app left the foreground, for a link
        // delivered before onStart), or already started by an onStart that
        // ran first (API 30 delivers onNewIntent after onStart, and then
        // landed every backgrounded tile click on Home).
        showPlayback(itemId, position)
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
        // Most callers are coroutines resuming after a network call (login,
        // logout, pairing poll), so the activity can already have saved state
        // — commit() then throws. Nothing is lost by dropping the navigation:
        // the SCREEN_ON / onStart path re-routes on the way back in.
        if (supportFragmentManager.isStateSaved) return
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

        // LOGIN and SERVER_SETUP are the roots of the signed-out flow, not
        // steps within it. Sign-out reached them via addToBackStack, so BACK
        // from the login screen walked the user back INTO the signed-out app —
        // and a second sign-out stacked a second copy. Clearing first makes
        // them terminal in the same way HOME is.
        if (destination == NavigationDestination.LOGIN ||
            destination == NavigationDestination.SERVER_SETUP
        ) {
            supportFragmentManager.popBackStack(
                null,
                androidx.fragment.app.FragmentManager.POP_BACK_STACK_INCLUSIVE,
            )
        }

        supportFragmentManager.beginTransaction()
            .replace(R.id.main_container, fragment)
            .apply {
                if (destination != NavigationDestination.HOME &&
                    destination != NavigationDestination.LOGIN &&
                    destination != NavigationDestination.SERVER_SETUP
                ) {
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
