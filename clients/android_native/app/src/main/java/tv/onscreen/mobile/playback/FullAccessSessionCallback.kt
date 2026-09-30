package tv.onscreen.mobile.playback

import androidx.media3.session.MediaSession
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture

/**
 * A session callback that accepts every controller with the full command set,
 * which is what Media3 gave every controller before 1.11. Both of the app's
 * sessions use it: PlaybackService's, and the video's key session in
 * PlayerScreen.
 *
 * Media3 1.11 cut a controller it doesn't trust down to read-only access. It
 * decides trust in-process (the system, this app, a holder of
 * MEDIA_CONTENT_CONTROL, an enabled notification listener), starting with a
 * package lookup — so a controller in a package this app cannot see (package
 * visibility, Android 11+) counts as untrusted. From outside the app either
 * session is reachable only through its platform session (PlaybackService is
 * not exported), and the controllers that connect there are media controls
 * such as Bluetooth, a watch or a car: exactly the ones a missed lookup would
 * quietly lock out of play, pause and skip.
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
