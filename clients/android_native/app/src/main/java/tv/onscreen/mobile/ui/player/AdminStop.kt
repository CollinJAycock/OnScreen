package tv.onscreen.mobile.ui.player

import androidx.media3.datasource.HttpDataSource
import com.squareup.moshi.Moshi
import tv.onscreen.mobile.data.api.ApiError
import tv.onscreen.mobile.data.model.PlaybackStop

// Error-path only; the generated ApiError adapter is found by reflection.
private val stopMoshi: Moshi by lazy { Moshi.Builder().build() }

/**
 * The sentence to show when a player error is the server refusing a media
 * request with `403 PLAYBACK_STOPPED` — an admin stopped this stream and the
 * direct-play range requests are refused for the stop window. Null for any
 * other failure (a real decode error, a network drop, a different 403), so
 * the caller keeps its normal error handling.
 *
 * ExoPlayer wraps the HTTP failure (PlaybackException → … →
 * [HttpDataSource.InvalidResponseCodeException]); DefaultHttpDataSource keeps
 * the error body, which carries the server's envelope. The web player gets
 * the same answer by re-probing the URL (probePlaybackStopped).
 */
fun playbackStoppedMessage(error: Throwable?): String? {
    var t: Throwable? = error
    var depth = 0
    while (t != null && depth < MAX_CAUSE_DEPTH) {
        if (t is HttpDataSource.InvalidResponseCodeException) {
            if (t.responseCode != 403) return null
            return playbackStoppedMessageFromBody(t.responseBody)
        }
        t = t.cause
        depth++
    }
    return null
}

/** [playbackStoppedMessage] for a raw 403 body: the display sentence when it
 *  is a PLAYBACK_STOPPED envelope, else null. */
internal fun playbackStoppedMessageFromBody(body: ByteArray?): String? {
    if (body == null || body.isEmpty()) return null
    val err = try {
        stopMoshi.adapter(ApiError::class.java).fromJson(String(body, Charsets.UTF_8))?.error
    } catch (_: Exception) {
        null
    } ?: return null
    if (err.code != PlaybackStop.ERROR_CODE) return null
    return PlaybackStop.textFromServer(err.message)
}

private const val MAX_CAUSE_DEPTH = 8
