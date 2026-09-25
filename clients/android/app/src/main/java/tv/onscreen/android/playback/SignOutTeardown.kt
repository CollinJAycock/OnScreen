package tv.onscreen.android.playback

import android.content.Context
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.AuthRepository
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Device-level teardown for an INVOLUNTARY sign-out, giving it parity with the
 * voluntary path in SettingsViewModel.logout.
 *
 * TokenAuthenticator clears the tokens when the refresh is definitively
 * rejected (an admin revoked a lost/handed-on TV, the refresh expired, reuse
 * detection fired). Before this, that was ALL that happened: the previous
 * account's Continue Watching rows stayed on the launcher (in the system TV
 * provider, with working deep links), parked background audio kept streaming on
 * its already-issued stream token, and the in-process identity caches survived.
 *
 * Watched from the Application rather than MainActivity so it also fires when
 * no activity is alive — e.g. music parked in OnScreenMediaSessionService after
 * the user backed out of the app, whose progress PUT is what hits the 401.
 * TokenAuthenticator stays free of Android dependencies; this observes the
 * logged-in → logged-out edge it produces instead.
 */
@Singleton
class SignOutTeardown @Inject constructor(
    @ApplicationContext private val appContext: Context,
    private val prefs: ServerPrefs,
    private val watchNext: WatchNextManager,
    private val authRepo: AuthRepository,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    @Volatile private var started = false

    /** Begin watching for the logged-in → logged-out edge. Idempotent. */
    fun start() {
        if (started) return
        started = true
        scope.launch {
            var wasLoggedIn = prefs.isLoggedIn.first()
            prefs.isLoggedIn.collect { loggedIn ->
                if (wasLoggedIn && !loggedIn) runTeardown()
                wasLoggedIn = loggedIn
            }
        }
    }

    /** Every step is idempotent, so the extra run after a voluntary logout
     *  (which already did all of this before clearing tokens) is harmless. */
    private suspend fun runTeardown() {
        runCatching { AudioHandoff.stopAll(appContext) } // main thread: ExoPlayer
        runCatching { authRepo.onInvoluntarySignOut() }
        // Provider query + per-row delete is cross-process binder IPC — off main.
        withContext(Dispatchers.IO) { runCatching { watchNext.removeAll() } }
    }
}
