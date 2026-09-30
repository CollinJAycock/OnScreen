package tv.onscreen.android.playback

import androidx.media3.common.util.UnstableApi
import androidx.media3.session.MediaSession
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture

/**
 * A session callback that accepts every controller with the full command set,
 * which is what Media3 gave every controller before 1.11. The app's one
 * session, [OnScreenMediaSessionService]'s, uses it.
 *
 * Media3 1.11 cut a controller it doesn't trust down to read-only access. It
 * decides trust in-process (the system, this app, a holder of
 * MEDIA_CONTENT_CONTROL, an enabled notification listener), starting with a
 * package lookup — so a controller in a package this app cannot see (package
 * visibility, Android 11+) counts as untrusted. The service is not exported,
 * so from outside the app the session is reachable only through its platform
 * session, and the controllers that connect there are the TV's own: the
 * voice assistant, the system's media controls, Bluetooth headphones. Those
 * are exactly the ones a missed lookup would quietly lock out of play, pause
 * and skip. (Remote media keys don't depend on it: while the service shows
 * its notification, Media3 applies them through its own notification
 * controller, which is this app.)
 */
@UnstableApi
class FullAccessSessionCallback : MediaSession.Callback {
    override fun onConnectAsync(
        session: MediaSession,
        controller: MediaSession.ControllerInfo,
    ): ListenableFuture<MediaSession.ConnectionResult> =
        Futures.immediateFuture(
            MediaSession.ConnectionResult.AcceptedResultBuilder(session, controller)
                .setAvailableSessionCommands(MediaSession.ConnectionResult.DEFAULT_SESSION_COMMANDS)
                .setAvailablePlayerCommands(MediaSession.ConnectionResult.DEFAULT_PLAYER_COMMANDS)
                .build(),
        )
}
