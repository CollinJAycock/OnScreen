package tv.onscreen.android.data.api

import tv.onscreen.android.data.model.PlaybackStopData

/**
 * Admin "stop this stream" — the viewer side.
 *
 * POST /api/v1/sessions/{id}/stop (admin, Now Playing) does two things the
 * player has to answer to (server: internal/api/v1/playback_stop.go +
 * sessions.go):
 *
 *  1. It publishes a `playback.stop` event on the viewer's SSE channel:
 *     `{item_id, session_id?, client_name?, decision?, message?}`. The channel
 *     is per-USER, not per-device, so every one of the user's players receives
 *     it and each decides whether it is the target ([targets]).
 *  2. For direct play / remux it refuses that (user, item, client IP) for ~2
 *     minutes: the 'playing' progress beacon, media bytes and transcode start
 *     answer `403 {"error":{"code":"PLAYBACK_STOPPED","message":<sentence>}}`.
 *     So a player that missed the event still stops on its next heartbeat
 *     (see [HeartbeatRefusal.PlaybackStopped]).
 *
 * Both paths end in the same player sentinel ([sentinel]) so PlaybackFragment
 * stops playback and shows one message: "Playback was stopped by the server
 * admin: <message>". Mirrors web/src/lib/playback-stop.ts.
 */
object PlaybackStop {
    /** SSE event type the admin stop publishes. */
    const val EVENT_TYPE = "playback.stop"

    /** 403 error code on progress / media bytes / transcode start while a
     *  stopped stream is inside its refusal window. */
    const val ERROR_CODE = "PLAYBACK_STOPPED"

    /** Player error-dialog sentinel prefix; the full sentence follows it. */
    const val SENTINEL_PREFIX = "playback_stopped:"

    /**
     * Does this stop event target this player? Same rules as the web
     * player's `isStopForPlayer`:
     *  - a different item (or nothing loaded) → no;
     *  - session_id set → the player whose transcode/remux session it is, or
     *    failing that the one whose client name matches;
     *  - client_name set → the player reporting that name in its heartbeats;
     *  - neither → untargeted: every player of that item stops.
     */
    fun targets(
        evt: PlaybackStopData,
        playingItemId: String?,
        sessionId: String?,
        clientName: String,
    ): Boolean {
        if (playingItemId.isNullOrEmpty() || evt.item_id != playingItemId) return false
        val nameMatches = !evt.client_name.isNullOrEmpty() && evt.client_name == clientName
        if (!evt.session_id.isNullOrEmpty()) {
            return evt.session_id == sessionId || nameMatches
        }
        if (!evt.client_name.isNullOrEmpty()) return nameMatches
        return true
    }

    /** The sentence the player shows — the same wording the server's 403
     *  uses. [message] is the admin's optional note. */
    fun text(message: String?): String {
        val m = message?.trim().orEmpty()
        return if (m.isEmpty()) {
            "Playback was stopped by the server admin."
        } else {
            "Playback was stopped by the server admin: $m"
        }
    }

    /** The sentence carried by a 403 PLAYBACK_STOPPED — the server already
     *  phrases it; fall back to the generic sentence when it's missing. */
    fun textFromServer(serverMessage: String?): String =
        serverMessage?.trim()?.takeIf { it.isNotEmpty() } ?: text(null)

    /** Wrap [sentence] as a player error sentinel. */
    fun sentinel(sentence: String): String = SENTINEL_PREFIX + sentence

    fun isSentinel(value: String?): Boolean = value?.startsWith(SENTINEL_PREFIX) == true

    /** The sentence inside a [sentinel] (or the generic one if it's empty). */
    fun sentenceOf(sentinel: String): String =
        sentinel.removePrefix(SENTINEL_PREFIX).trim().ifEmpty { text(null) }
}
