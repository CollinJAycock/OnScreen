package tv.onscreen.android.playback

import tv.onscreen.android.data.model.TranscodeSession

/**
 * A server stream session (remux or transcode) a player reads its HLS from.
 * The server runs ffmpeg for as long as the session lives, so it has exactly
 * one owner at a time, and that owner is whoever holds the player:
 *
 *  - PlaybackViewModel, while PlaybackFragment drives the player;
 *  - the [AudioHandoff] slot, while an audio player is parked there;
 *  - [OnScreenMediaSessionService], while the player plays in the background.
 *
 * Passing the player on passes the session with it. Letting the player go for
 * good (releasing it, or moving it to another item) ends the session. The
 * fragment used to end it on teardown even after parking the player, so
 * transcoded or remuxed audio stopped seconds after BACK or HOME.
 */
data class StreamSession(
    val id: String,
    val token: String,
    /** Content time the stream opens at: player position 0 is this far
     *  into the item. */
    val offsetMs: Long,
    /** The session's playlist, clean: its `?token=` is in [StreamTokenVault]
     *  and the player's resolving data source re-attaches it. */
    val playlistUrl: String,
) {
    companion object {
        /** The session [started] opened. Its playlist token goes into the
         *  vault, and its offset is the server's keyframe-aligned
         *  `start_offset_sec`, or [requestedMs] from a server that omits it.
         *  A real 0 is kept, as on the phone: a resume a few seconds in, before
         *  the first keyframe after 0:00, or a stream that covers the whole
         *  file (a pre-encoded ladder), opens at the very start. Read as "not
         *  sent", it put the offset at the resume point over a stream that
         *  starts at 0:00, and every content time on the screen was off by it. */
        fun opened(started: TranscodeSession, serverUrl: String, requestedMs: Long): StreamSession {
            val offsetMs = started.start_offset_sec?.takeIf { it >= 0.0 }
                ?.let { (it * 1000.0).toLong() }
                ?: requestedMs
            val (clean, token) = StreamTokenVault.split("$serverUrl${started.playlist_url}")
            return StreamSession(
                id = started.session_id,
                token = started.token,
                offsetMs = offsetMs,
                playlistUrl = StreamTokenVault.register(clean, token),
            )
        }
    }
}
