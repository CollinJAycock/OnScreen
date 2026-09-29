package tv.onscreen.android.playback

import androidx.media3.common.C
import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi

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
     *  the item's duration wins whenever it is known, as it does for the
     *  progress reports (PlaybackHelper.contentDurationMs). */
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
