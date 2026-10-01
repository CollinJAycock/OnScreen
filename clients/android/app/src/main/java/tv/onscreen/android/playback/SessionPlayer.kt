package tv.onscreen.android.playback

import androidx.media3.common.C
import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import java.util.IdentityHashMap

/**
 * The background player as its media session presents it to the system
 * (the Fire TV / Google TV now-playing UI, remotes, voice assistants).
 * Two things differ from the ExoPlayer underneath:
 *
 *  - Time. A server session opened at a resume point starts its timeline
 *    there, so the player's position 0 is [offsetMs] into the item. The
 *    session reports positions and duration in the item's own time, and
 *    turns a seek in that time back into player time, as the fragment's
 *    scrubber does. With no offset (direct play) everything passes through.
 *  - Names. The fragment's media items carry no title or artist, so the
 *    session names the item from [nowPlaying] (see [NowPlaying]).
 *  - The default position, and Next. To the player, a server session still
 *    being written is a live stream: its default position trails the live
 *    edge (TranscodeHls.mediaItem), and Next with no next item goes there.
 *    The session takes the default for the start of what the player holds,
 *    and has no such Next, as for a direct play — its listeners included.
 *
 * The lambdas are read on every call, so a chain to the next item (new
 * offset, duration and names) shows at once.
 */
@UnstableApi
class SessionPlayer(
    player: Player,
    private val offsetMs: () -> Long,
    private val itemDurationMs: () -> Long?,
    private val nowPlaying: () -> NowPlaying?,
) : ForwardingPlayer(player) {

    override fun getCurrentPosition(): Long = super.getCurrentPosition() + offsetMs()

    override fun getContentPosition(): Long = super.getContentPosition() + offsetMs()

    override fun getBufferedPosition(): Long = super.getBufferedPosition() + offsetMs()

    override fun getContentBufferedPosition(): Long = super.getContentBufferedPosition() + offsetMs()

    override fun getDuration(): Long = contentDuration(super.getDuration())

    override fun getContentDuration(): Long = contentDuration(super.getContentDuration())

    override fun seekTo(positionMs: Long) = super.seekTo(toPlayerTime(positionMs))

    override fun seekTo(mediaItemIndex: Int, positionMs: Long) =
        super.seekTo(mediaItemIndex, toPlayerTime(positionMs))

    /** A seek to 0 in item time: Play from the system's media controls once
     *  the item has ended (Util.handlePlayButtonAction, which seeks to the
     *  default position) replays it from the session's start. */
    override fun seekToDefaultPosition() = seekTo(0L)

    override fun seekToDefaultPosition(mediaItemIndex: Int) = seekTo(mediaItemIndex, 0L)

    /** Next only to a next item, which the player never has: Next on a
     *  server session still being written went to its default position, up
     *  to minutes back, and the system's media controls offered it for a
     *  transcoded track only. The next track comes when this one ends (the
     *  service's chain). */
    override fun seekToNext() {
        if (super.hasNextMediaItem()) super.seekToNext()
    }

    override fun isCommandAvailable(command: Int): Boolean =
        if (command == Player.COMMAND_SEEK_TO_NEXT) nextAvailable() else super.isCommandAvailable(command)

    override fun getAvailableCommands(): Player.Commands {
        val commands = super.getAvailableCommands()
        if (nextAvailable() || !commands.contains(Player.COMMAND_SEEK_TO_NEXT)) return commands
        return commands.buildUpon().remove(Player.COMMAND_SEEK_TO_NEXT).build()
    }

    private fun nextAvailable(): Boolean =
        super.hasNextMediaItem() && super.isCommandAvailable(Player.COMMAND_SEEK_TO_NEXT)

    /** Each listener added hears the session's commands, not the player's
     *  (see [CommandsListener]): the media session takes the ones the
     *  player's event carries, which still offered the Next it has none of.
     *  Kept by identity, so removing the one added removes it. */
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

    override fun getMediaMetadata(): MediaMetadata {
        val names = nowPlaying() ?: return super.getMediaMetadata()
        return names.toMediaMetadata(super.getMediaMetadata())
    }

    override fun getCurrentMediaItem(): MediaItem? {
        val item = super.getCurrentMediaItem() ?: return null
        val names = nowPlaying() ?: return item
        return item.buildUpon()
            .setMediaId(names.mediaId.ifEmpty { item.mediaId })
            .setMediaMetadata(names.toMediaMetadata(item.mediaMetadata))
            .build()
    }

    /** The item's own length. A resumed session's player only knows the
     *  remaining part, and a session still being transcoded (from any
     *  point, the start included) only what has been produced so far, so
     *  the listed length (the file's, else the item's runtime) wins
     *  whenever it is known. The progress reports read the same one, save
     *  that a direct play's own settled duration comes first there
     *  (PlaybackHelper.contentDurationMs): the same file's length, as the
     *  player reads it. */
    private fun contentDuration(playerDuration: Long): Long {
        val item = itemDurationMs()
        if (item != null && item > 0L) return item
        val offset = offsetMs()
        if (offset <= 0L || playerDuration == C.TIME_UNSET) return playerDuration
        return playerDuration + offset
    }

    /** Item time → player time. The session can't go back before the point
     *  it opened at, so earlier seeks land at its start. */
    private fun toPlayerTime(contentMs: Long): Long = (contentMs - offsetMs()).coerceAtLeast(0L)
}
