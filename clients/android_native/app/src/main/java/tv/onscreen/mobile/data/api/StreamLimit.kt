package tv.onscreen.mobile.data.api

import retrofit2.HttpException

/**
 * An admin's per-user concurrent-stream limit (v2.6). The play decision
 * (POST /items/{id}/playback-decision) and transcode start both refuse a
 * stream past it with 429 `TOO_MANY_SESSIONS` and a sentence written for the
 * viewer ("Your account can play one stream at a time, and another device is
 * already playing. Stop it to watch here."). The player shows that sentence
 * under [TITLE] and stops there: no fallback to the local decision (that
 * would start the very stream the server just refused) and no automatic
 * retry (only stopping another device frees a slot).
 *
 * Other 429s (a route's rate limiter) are not this, and keep their own
 * handling.
 */
object StreamLimit {

    const val CODE = "TOO_MANY_SESSIONS"

    /** Title of the error the player shows. */
    const val TITLE = "Too many streams"

    /** Shown when the server's refusal carries no message of its own. */
    const val FALLBACK_MESSAGE =
        "Your account is already playing as many streams as it may. Stop one on another device to watch here."

    /** The viewer-ready message when [httpCode] / [error] are the stream-limit
     *  refusal, else null. */
    fun message(httpCode: Int, error: ErrorBody?): String? {
        if (httpCode != 429 || error?.code != CODE) return null
        return error.message?.takeIf { it.isNotBlank() } ?: FALLBACK_MESSAGE
    }

    /** [message] for a failed call. Reads the error body, which is single-read:
     *  only for a caller that needs nothing else from it. */
    fun messageOf(e: Throwable): String? {
        val http = e as? HttpException ?: return null
        if (http.code() != 429) return null
        return message(429, http.apiError())
    }
}
