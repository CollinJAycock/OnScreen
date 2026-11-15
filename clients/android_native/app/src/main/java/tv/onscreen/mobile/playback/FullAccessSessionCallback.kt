package tv.onscreen.mobile.playback

import androidx.media3.session.MediaSession
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture

/**
 * A session callback that accepts every controller with the full command set,
 * which is what Media3 gave every controller before 1.11. The video's key
 * session in PlayerScreen uses it as it is; PlaybackService's
 * (CarLibraryCallback) builds on it but gives an app it doesn't let browse
 * less (no queue commands).
 *
 * Media3 1.11 cut a controller it doesn't trust down to read-only access. It
 * decides trust in-process (the system, this app, a holder of
 * MEDIA_CONTENT_CONTROL, an enabled notification listener), starting with a
 * package lookup — so a controller in a package this app cannot see (package
 * visibility, Android 11+) counts as untrusted. The video session is
 * reachable from outside the app only through its platform session, and the
 * controllers that connect there are media controls such as Bluetooth or a
 * watch: exactly the ones a missed lookup would quietly lock out of play,
 * pause and skip. PlaybackService, by contrast, is exported (Android Auto
 * binds it), so any installed app can connect to it — see CarLibraryCallback.
 */
open class FullAccessSessionCallback : MediaSession.Callback {
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
