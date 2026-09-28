package tv.onscreen.mobile.data.model

/**
 * Admin "stop this stream" (Now Playing → Stop), viewer side.
 *
 * POST /api/v1/sessions/{id}/stop publishes a `playback.stop` event on the
 * viewer's SSE channel (internal/api/v1/playback_stop.go):
 *
 *     { item_id, session_id?, client_name?, decision?, message? }
 *
 * and, for direct play / direct stream / remux, refuses that user + item +
 * client IP for ~2 minutes with 403 `PLAYBACK_STOPPED` on media bytes,
 * transcode start and 'playing' progress heartbeats — so a player that
 * misses the event still stops at its next heartbeat.
 *
 * The SSE channel is per-USER, so every one of the user's players receives
 * the event; each decides with [targets] whether it is the one being
 * stopped. Mirrors web/src/lib/playback-stop.ts.
 */
data class PlaybackStopEvent(
    val item_id: String,
    val session_id: String? = null,
    val client_name: String? = null,
    val decision: String? = null,
    val message: String? = null,
) {
    /**
     * Does this event target the player that is playing [itemId] on
     * transcode/remux session [sessionId] (null for direct play) and reports
     * [clientName] in its heartbeats (null when it reports none)?
     *   - session_id set  → the player owning that session, or failing that
     *                       the one whose client name matches;
     *   - client_name set → the player reporting that name;
     *   - neither         → untargeted: any player on that item stops.
     */
    fun targets(itemId: String?, sessionId: String?, clientName: String?): Boolean {
        if (itemId.isNullOrEmpty() || item_id != itemId) return false
        val nameMatches = !client_name.isNullOrEmpty() && client_name == clientName
        if (!session_id.isNullOrEmpty()) return session_id == sessionId || nameMatches
        if (!client_name.isNullOrEmpty()) return nameMatches
        return true
    }

    /** The sentence the player shows — same wording as the server's 403. */
    val displayText: String get() = PlaybackStop.text(message)
}

object PlaybackStop {
    /** SSE `type` of the admin stop event. */
    const val EVENT_TYPE = "playback.stop"

    /** Error code of the 403 a stopped stream gets for ~2 minutes. */
    const val ERROR_CODE = "PLAYBACK_STOPPED"

    /** "Playback was stopped by the server admin[: message]" — the wording the
     *  server's own 403 message uses (playbackStoppedText). */
    fun text(message: String?): String {
        val m = message?.trim().orEmpty()
        return if (m.isEmpty()) {
            "Playback was stopped by the server admin."
        } else {
            "Playback was stopped by the server admin: $m"
        }
    }

    /** Display text for a 403 PLAYBACK_STOPPED whose error message is
     *  [serverMessage]. The server already phrases it as the full sentence;
     *  fall back to the bare sentence when it's missing. */
    fun textFromServer(serverMessage: String?): String =
        serverMessage?.trim()?.takeIf { it.isNotEmpty() } ?: text(null)
}
