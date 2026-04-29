package tv.onscreen.android.playback

import android.content.Intent
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.session.MediaSession
import androidx.media3.session.MediaSessionService

/**
 * Media3 session service. Hosts the audio player when the user
 * navigates away from the PlaybackFragment for music — without
 * this, backing out of the player kills the audio.
 *
 * Lifecycle:
 *  - PlaybackFragment hands its audio player to the service via
 *    [attach] before navigating away. The service starts itself
 *    foreground (Media3 manages the notification automatically when
 *    a session has playWhenReady=true) and keeps playing.
 *  - When playback ends or pauses past the auto-stop window, the
 *    service stops itself. The Media3 framework also surfaces
 *    play/pause/skip on the system media-session rail (Bluetooth
 *    headphones, lockscreen, Watch Next, Now Playing on Android
 *    Auto if the device exposes one).
 *  - Re-entering PlaybackFragment for a track that's already
 *    playing in the service returns the same player instance via
 *    [activePlayer]; the fragment binds it to the Leanback glue
 *    instead of building a fresh ExoPlayer, so playback is
 *    seamless.
 *
 * Video playback intentionally skips this path — the floating PiP
 * window is the right "keep watching outside the player" UX for
 * video, and the surface-view rendering doesn't translate to a
 * service notification.
 */
@UnstableApi
class OnScreenMediaSessionService : MediaSessionService() {

    private var session: MediaSession? = null

    override fun onCreate() {
        super.onCreate()
        // Pick up the player PlaybackFragment parked in the
        // process-wide AudioHandoff slot. If the service was
        // resurrected by the system without a fresh handoff (e.g. a
        // Bluetooth media-button press after the app was swiped
        // away) peek() returns null and we have nothing to bind
        // until the next attach() call.
        AudioHandoff.peek()?.let { attach(it) }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        // Re-check the handoff slot in case the service was already
        // alive when the fragment parked a fresh player — the
        // onCreate hook only fires once per process lifetime.
        AudioHandoff.peek()?.let { p ->
            if (session?.player !== p) attach(p)
        }
        return super.onStartCommand(intent, flags, startId)
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaSession? = session

    override fun onTaskRemoved(rootIntent: Intent?) {
        // When the user swipes the app away from the recents tray
        // we release everything — there's nothing to bring back to
        // the foreground for, and dangling media sessions clutter
        // the system controls.
        val player = session?.player
        if (player != null && (!player.playWhenReady || player.mediaItemCount == 0)) {
            stopSelf()
        }
    }

    override fun onDestroy() {
        session?.run {
            player.release()
            release()
        }
        session = null
        super.onDestroy()
    }

    /** Bind an externally-owned ExoPlayer to a MediaSession exposed
     *  by this service. Called by PlaybackFragment when it's about
     *  to be destroyed but the user is still listening to music —
     *  hand the player here, the fragment loses its reference, and
     *  the service keeps it playing under the system controls. */
    fun attach(player: ExoPlayer) {
        session?.let {
            it.player.release()
            it.release()
        }
        // Audio focus + becoming-noisy handling: the Media3 player
        // builder defaults already cover both, so we don't need
        // to wire AudioManager.OnAudioFocusChangeListener.
        player.setAudioAttributes(
            AudioAttributes.Builder()
                .setUsage(C.USAGE_MEDIA)
                .setContentType(C.AUDIO_CONTENT_TYPE_MUSIC)
                .build(),
            /* handleAudioFocus = */ true,
        )
        session = MediaSession.Builder(this, player).build()
    }

    /** Detach and return the active player. Called when
     *  PlaybackFragment is re-entering for the same item — the
     *  fragment takes the player back, attaches it to its Leanback
     *  glue, and the service shuts down (no second player needed). */
    fun detach(): ExoPlayer? {
        val p = session?.player as? ExoPlayer ?: return null
        session?.release()
        session = null
        // Don't release the player — the caller now owns it.
        stopSelf()
        return p
    }

    /** Whether a player is currently parked in the service. The
     *  fragment uses this on entry to decide between "rebind to the
     *  service's player" vs "build a fresh ExoPlayer". */
    fun hasActivePlayer(): Boolean = session?.player != null
}
