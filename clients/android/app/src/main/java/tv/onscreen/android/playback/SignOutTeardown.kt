package tv.onscreen.android.playback

import android.content.Context
import coil.imageLoader
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.AuthRepository
import javax.inject.Inject
import javax.inject.Singleton
import tv.onscreen.android.data.backgroundScope

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
 * It also stops music playing on the now-playing screen, and drops the image
 * caches (the previous account's artwork).
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
    private val scope = backgroundScope("SignOutTeardown", Dispatchers.Main)
    @Volatile private var started = false

    /** Begin watching for the logged-in → logged-out edge. Idempotent. */
    fun start() {
        if (started) return
        started = true
        scope.launch {
            var wasLoggedIn = prefs.isLoggedIn.first()
            AudioHandoff.signedOut = !wasLoggedIn
            prefs.isLoggedIn.collect { loggedIn ->
                if (wasLoggedIn && !loggedIn) runTeardown()
                // Signed in again: background audio is allowed again.
                if (loggedIn) AudioHandoff.signedOut = false
                wasLoggedIn = loggedIn
            }
        }
    }

    /** Every step is idempotent, so the extra run after a voluntary logout
     *  (which already did some of this before clearing tokens) is harmless. */
    private suspend fun runTeardown() {
        // Main thread: ExoPlayer. The player on screen goes first, and from
        // here on nothing is parked or starts the service (see
        // AudioHandoff.signedOut).
        runCatching { AudioHandoff.haltForSignOut(appContext) }
        runCatching { authRepo.onInvoluntarySignOut() }
        // The previous account's artwork: posters from libraries the next
        // person may not see, rated art above their ceiling.
        runCatching { clearImageCaches(appContext) }
        // Provider query + per-row delete is cross-process binder IPC — off main.
        withContext(Dispatchers.IO) { runCatching { watchNext.removeAll() } }
    }
}

/**
 * Drop every cached image: Coil's memory cache and its on-disk artwork
 * cache (OnScreenApp.newImageLoader). Run on a sign-out (SignOutTeardown) and
 * when a different server is set up (ServerSetupFragment). The disk clear is
 * file I/O, so it runs off the main thread.
 */
suspend fun clearImageCaches(context: Context) {
    val loader = context.imageLoader
    loader.memoryCache?.clear()
    withContext(Dispatchers.IO) { runCatching { loader.diskCache?.clear() } }
}
