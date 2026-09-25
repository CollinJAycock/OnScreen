package tv.onscreen.mobile.playback

import android.content.Context
import android.content.Intent
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.AuthRepository
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Device-level teardown for an INVOLUNTARY sign-out, giving it parity with the
 * voluntary path in SettingsViewModel.signOut. Ported from the TV client's
 * `playback/SignOutTeardown.kt`.
 *
 * TokenAuthenticator clears the tokens (and downloads) when the refresh is
 * definitively rejected — an admin revoked a lost/sold phone, the refresh
 * expired, reuse detection fired. Before this, that was ALL that happened:
 * music in [PlaybackService] (a process-lifetime MediaSessionService nothing
 * else stops) kept streaming on the revoked user's already-issued stream token
 * with their track on the lock screen, [StreamTokenVault] kept re-attaching
 * that token, and the in-process identity caches survived for the next account.
 *
 * Watched from the Application rather than an Activity so it also fires when no
 * UI is alive — the background heartbeat of that very PlaybackService is often
 * what hits the 401. TokenAuthenticator is built inside the OkHttp graph and
 * cannot depend on AuthRepository (Hilt cycle), so this observes the
 * logged-in → logged-out edge it produces instead.
 */
@Singleton
class SignOutTeardown @Inject constructor(
    @ApplicationContext private val appContext: Context,
    private val prefs: ServerPrefs,
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

    /** Every step is idempotent, so the extra run after a voluntary sign-out
     *  (which already stopped audio before clearing tokens) is harmless. */
    private fun runTeardown() {
        runCatching { stopBackgroundAudio(appContext) }
        runCatching { authRepo.onInvoluntarySignOut() }
    }

    companion object {
        /** Tear down the background-audio service and drop cached playback
         *  credentials. Shared by the voluntary (SettingsViewModel) and
         *  involuntary (this class) sign-out paths so both stop identically.
         *
         *  stopService rather than a controller command: PlayerScreen releases
         *  only its MediaController on back-out ("backing out keeps audio
         *  playing"), so the service outlives every UI surface. stopService
         *  alone cannot destroy it while a controller is still bound (the
         *  MiniPlayerBar's, for as long as the activity lives), which is why
         *  PlaybackService also halts its own player on the same sign-out edge. */
        fun stopBackgroundAudio(context: Context) {
            runCatching {
                context.stopService(Intent(context, PlaybackService::class.java))
            }
            // Stream/asset tokens keyed by URL. Short-lived and in-memory only,
            // but the next account on this device has no business with them.
            StreamTokenVault.clear()
        }
    }
}
