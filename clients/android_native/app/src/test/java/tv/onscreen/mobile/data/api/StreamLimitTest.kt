package tv.onscreen.mobile.data.api

import com.google.common.truth.Truth.assertThat
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response

/** 429 TOO_MANY_SESSIONS — an admin's per-user concurrent-stream limit. */
class StreamLimitTest {

    private fun http(code: Int, body: String) = HttpException(
        Response.error<Any>(code, body.toResponseBody("application/json".toMediaTypeOrNull())),
    )

    private val limitBody = """{"error":{"code":"TOO_MANY_SESSIONS",""" +
        """"message":"Your account can play one stream at a time, and another device is already playing. Stop it to watch here."}}"""

    @Test
    fun `the server's sentence is shown as it is`() {
        assertThat(StreamLimit.messageOf(http(429, limitBody))).isEqualTo(
            "Your account can play one stream at a time, and another device is already playing. Stop it to watch here.",
        )
    }

    @Test
    fun `a refusal without a message gets the fallback sentence`() {
        val body = """{"error":{"code":"TOO_MANY_SESSIONS","message":""}}"""
        assertThat(StreamLimit.messageOf(http(429, body))).isEqualTo(StreamLimit.FALLBACK_MESSAGE)
    }

    @Test
    fun `other 429s are not the stream limit`() {
        // A route's rate limiter, or a download queue that's full.
        assertThat(StreamLimit.messageOf(http(429, """{"error":{"code":"RATE_LIMITED","message":"slow down"}}"""))).isNull()
        assertThat(StreamLimit.messageOf(http(429, "not json"))).isNull()
    }

    @Test
    fun `the code on another status is not the stream limit`() {
        assertThat(StreamLimit.message(403, ErrorBody(code = "TOO_MANY_SESSIONS", message = "x", request_id = null))).isNull()
        assertThat(StreamLimit.messageOf(IllegalStateException("offline"))).isNull()
    }
}
