package tv.onscreen.mobile.ui.player

import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.MediaSource
import java.util.IdentityHashMap

/**
 * The screen-owned player of a remux / transcode session ([PlaybackSource.Hls])
 * as the screen drives it: the PlayerView and its controls, the media-key
 * session (KeysOnlySessionPlayer wraps this) and the error overlay's Retry.
 * Direct play and offline files use their ExoPlayer as it is.
 *
 * It keeps Media3 off the live window's default position. A session playlist
 * is live until its ENDLIST, and [hlsMediaItem] puts that default at the
 * stream's start for the first playlist load only: HlsMediaSource keeps the
 * target offset it clamped then (that playlist's length), so once the
 * playlist grows the default trails the window's end by that much — seconds
 * to minutes into the stream, as often behind the viewer as ahead. Media3
 * goes there
 *  - for Play on an ended stream (Util.handlePlayButtonAction: the
 *    controller's button, a headset key). Here it plays the stream again from
 *    its start, as an ended file does;
 *  - for Next on a live window with no next item (BasePlayer.seekToNext). A
 *    session is one item, so Next is withdrawn, from the commands events
 *    too: the controller shows the button disabled, as for a file, and a
 *    Next key does nothing;
 *  - when the player prepares again after an error (Retry, or Play on the
 *    controller or a key). MaskingMediaSource takes a start equal to the old
 *    window's default for "no position asked for" and moves it to the new
 *    window's, so a retry at 0 after an error before the playlist first grew
 *    (when that default was still 0) started as far in as the playlist had
 *    grown since. [prepare] hands the player a new source from [newSource]
 *    instead, whose first load puts the default at the start again.
 */
internal class HlsSessionPlayer(
    private val exoPlayer: ExoPlayer,
    private val newSource: () -> MediaSource,
) : ForwardingPlayer(exoPlayer) {

    override fun seekToDefaultPosition() = seekTo(currentMediaItemIndex, 0L)

    override fun seekToDefaultPosition(mediaItemIndex: Int) = seekTo(mediaItemIndex, 0L)

    /** Nothing: the command is withdrawn. Kept for a caller that doesn't
     *  check it. */
    override fun seekToNext() {}

    override fun isCommandAvailable(command: Int): Boolean =
        command != Player.COMMAND_SEEK_TO_NEXT && super.isCommandAvailable(command)

    override fun getAvailableCommands(): Player.Commands =
        super.getAvailableCommands().buildUpon().remove(Player.COMMAND_SEEK_TO_NEXT).build()

    /** Each listener added hears these commands, not the player's (see
     *  [CommandsListener]): the key session takes the ones the player's event
     *  carries, which still offered Next to its controllers. Kept by
     *  identity, so removing the one added removes it. */
    private val listeners = IdentityHashMap<Player.Listener, Player.Listener>()

    override fun addListener(listener: Player.Listener) {
        val heard = synchronized(listeners) {
            listeners.getOrPut(listener) { CommandsListener(listener) { availableCommands } }
        }
        super.addListener(heard)
    }

    override fun removeListener(listener: Player.Listener) {
        val heard = synchronized(listeners) { listeners.remove(listener) }
        super.removeListener(heard ?: listener)
    }

    /** An idle player here is one an error (or the key session's stop) has
     *  stopped: it prepares again where it stopped, on a new source. */
    override fun prepare() {
        if (playbackState == Player.STATE_IDLE) exoPlayer.setMediaSource(newSource(), currentPosition)
        super.prepare()
    }
}
