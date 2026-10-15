package tv.onscreen.android.data.api

import retrofit2.HttpException

/**
 * An admin's per-user concurrent-stream limit (server v2.6,
 * internal/api/v1/stream_limit.go). The play decision and transcode start
 * refuse a stream over the limit with
 * `429 {"error":{"code":"TOO_MANY_SESSIONS","message":<sentence>}}`; the
 * message is written for the viewer ("Your account can play one stream at a
 * time, and another device is already playing…").
 *
 * The player shows it on a "Too many streams" dialog and stops there: no
 * fallback to the local play decision (that would start the very stream the
 * server refused) and no automatic retry.
 */
object StreamLimit {
    const val ERROR_CODE = "TOO_MANY_SESSIONS"

    /** Player error-dialog sentinel prefix; the sentence follows it. */
    const val SENTINEL_PREFIX = "too_many_streams:"

    /** Shown when the server sent no sentence of its own. */
    const val DEFAULT_TEXT =
        "Your account is already playing as many streams as it can. Stop one on another device to watch here."

    /** Whether an error answer is the stream-limit refusal. */
    fun isRefusal(httpCode: Int, errorCode: String?): Boolean =
        httpCode == 429 && errorCode == ERROR_CODE

    /** The sentence to show for the server's [message]. */
    fun textFromServer(message: String?): String =
        message?.trim()?.takeIf { it.isNotEmpty() } ?: DEFAULT_TEXT

    fun sentinel(text: String): String = SENTINEL_PREFIX + text

    fun isSentinel(value: String?): Boolean = value?.startsWith(SENTINEL_PREFIX) == true

    fun textOf(sentinel: String): String = sentinel.removePrefix(SENTINEL_PREFIX)

    /**
     * The refusal [e] carries, if it is one. Only a 429's body is read (an
     * error body reads once): any other status is left for its caller to
     * parse ([apiError]).
     */
    fun of(e: HttpException): TooManyStreamsException? {
        if (e.code() != 429) return null
        val err = e.apiError() ?: return null
        return if (isRefusal(e.code(), err.code)) TooManyStreamsException(textFromServer(err.message)) else null
    }
}

/** The server refused a stream for the user's concurrent-stream limit
 *  ([StreamLimit]). [message] is the sentence to show. */
class TooManyStreamsException(override val message: String) : Exception(message)
