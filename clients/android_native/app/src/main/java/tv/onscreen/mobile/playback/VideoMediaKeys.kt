package tv.onscreen.mobile.playback

import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.Player

/*
 * Media keys (a headset or Bluetooth play/pause, `cmd media_session
 * dispatch`) go to ONE media session: the app's most recently active one.
 * PlaybackService's session used to be the only one this app had, and it
 * stays active for as long as the mini player keeps a controller bound — so
 * a play key during a video went to an ended or paused audiobook. Media3
 * plays an ENDED player from its start (and prepares an IDLE one), the
 * service's audio-focus request then paused the video, and ten seconds later
 * the service's heartbeat saved the book's resume point as ~0.
 *
 * The screen-owned player now publishes its own session (PlayerScreen), so
 * the keys pause and resume the video; [ServiceMediaKeys] is the backstop in
 * the service for whatever still reaches it.
 */

/**
 * The player a screen-owned player's MediaSession is built on: the real one,
 * with its current item, timeline and metadata hidden from the session, and
 * starting only while [canStart] (the player's screen is up).
 *
 * The session lives as long as the player screen, and that includes the app
 * in the background, where ON_STOP has paused the video. A headset play then
 * started the video's sound with nothing on screen and no notification to
 * stop it. Pausing always works.
 *
 * The session exists only to take media keys. What it must not do is
 * republish the item: media3's legacy bridge copies
 * `MediaItem.localConfiguration.uri` into the platform session's
 * METADATA_KEY_MEDIA_URI (and the timeline into its queue), where any
 * notification-listener app can read it — see [StreamTokenVault]. A direct-
 * play url is clean, but a transcode or remux playlist url carries its
 * session token in the query, and an offline one is a file path. Every read
 * the bridge makes goes through these command checks
 * (PlayerWrapper.get…WithCommandCheck), so hiding the three commands hides the
 * item. Play/pause and seeking are untouched.
 */
class KeysOnlySessionPlayer(
    player: Player,
    private val canStart: () -> Boolean = { true },
) : ForwardingPlayer(player) {

    override fun play() {
        if (canStart()) super.play()
    }

    override fun setPlayWhenReady(playWhenReady: Boolean) {
        if (!playWhenReady || canStart()) super.setPlayWhenReady(playWhenReady)
    }

    override fun isCommandAvailable(command: Int): Boolean =
        command !in HIDDEN && super.isCommandAvailable(command)

    override fun getAvailableCommands(): Player.Commands =
        super.getAvailableCommands().buildUpon().removeAll(*HIDDEN).build()

    private companion object {
        val HIDDEN = intArrayOf(
            Player.COMMAND_GET_CURRENT_MEDIA_ITEM,
            Player.COMMAND_GET_TIMELINE,
            Player.COMMAND_GET_METADATA,
        )
    }
}

/**
 * Whether PlaybackService swallows a media button instead of acting on it —
 * the backstop for a key that reaches the service's session while a video is
 * on screen (see the note at the top of this file).
 *
 * Only while a video is up, and only when the key would START the service's
 * audio: audio that is audibly playing (resumed from the notification over a
 * paused video, say) still pauses. Nothing is stopped or cleared, so a paused
 * book stays resumable once the video closes.
 */
object ServiceMediaKeys {
    fun ignore(videoOnScreen: Boolean, playWhenReady: Boolean, playbackState: Int): Boolean {
        if (!videoOnScreen) return false
        val audible = playWhenReady &&
            (playbackState == Player.STATE_READY || playbackState == Player.STATE_BUFFERING)
        return !audible
    }
}
