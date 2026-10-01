package tv.onscreen.mobile.ui.player

/**
 * The full CONTENT duration progress is reported against — what the server
 * divides the position by to decide "watched" (past 90%) and whether there is
 * a resume point — and what the Up Next lead-in counts down to.
 *
 * Order: the player's duration when it can be trusted, then the playing
 * file's probed duration, then the item's, else [UNKNOWN].
 *
 * The position reported is time in the FILE, so the file's length is the
 * right divisor. The item's duration is metadata — a movie's or episode's
 * listed runtime (TMDB, whole minutes) — and can be minutes off the file in
 * hand: a runtime shorter than the file marked things watched early and
 * raised Up Next with minutes still to play; a longer one could keep a title
 * from ever reaching 90%. The scanner's own note says the same: the file's
 * duration is the authoritative one for the player. The item's is the last
 * resort, for a file whose probe found none.
 *
 * The player's duration is the file's exactly on a settled progressive source
 * (direct play, or a download). It is NOT the content's on an HLS session.
 * The server's remux / transcode playlist is an EVENT playlist that grows as
 * ffmpeg works, so until it ends the player's duration is just the few
 * minutes produced so far — and a resumed session only covers the rest of the
 * file anyway. On a device, resuming a movie over HLS whose item carried no
 * duration paired a content-absolute position (1 h in) with that window (a
 * few minutes): the server saw well past 90%, marked the movie watched and
 * cleared its resume point about four minutes in. A dynamic or live window
 * is untrustworthy for the same reason, whatever the source.
 *
 * With nothing trustworthy the answer is [UNKNOWN]: a server that keeps the
 * duration it knows then gets progress without one, an older server none at
 * all (see PlayerViewModel.reportProgress). Up Next gets no lead-in.
 */
object ContentDuration {

    const val UNKNOWN = 0L

    fun of(
        itemDurationMs: Long?,
        fileDurationMs: Long?,
        playerDurationMs: Long,
        playerDurationTrusted: Boolean,
    ): Long {
        // C.TIME_UNSET is negative, so > 0 also rules out "not known yet".
        if (playerDurationTrusted && playerDurationMs > 0) return playerDurationMs
        fileDurationMs?.takeIf { it > 0 }?.let { return it }
        itemDurationMs?.takeIf { it > 0 }?.let { return it }
        return UNKNOWN
    }

    /** Whether the player's duration is the content's: a progressive (direct
     *  play or downloaded) source whose window has settled — never an HLS
     *  session, a dynamic window or a live one. */
    fun playerDurationTrusted(hlsSession: Boolean, windowDynamic: Boolean, windowLive: Boolean): Boolean =
        !hlsSession && !windowDynamic && !windowLive
}
