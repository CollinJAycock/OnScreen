package tv.onscreen.android.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.android.data.api.ApiResponse
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.api.StreamLimit
import tv.onscreen.android.data.api.TooManyStreamsException
import tv.onscreen.android.data.api.apiError
import tv.onscreen.android.data.model.PlaybackDecision

/**
 * The play decision and transcode start against v2.6 servers: the
 * concurrent-stream limit (429 TOO_MANY_SESSIONS) must reach the player as a
 * refusal, never be swallowed into "no decision" (which falls back to the
 * local one and starts the refused stream anyway).
 */
class TranscodeRepositoryTest {

    private fun http(code: Int, body: String = "") =
        HttpException(Response.error<Unit>(code, body.toResponseBody(null)))

    private val tooMany =
        """{"error":{"code":"TOO_MANY_SESSIONS","message":"Your account can play one stream at a time."}}"""

    @Test
    fun `the decision carries an optimized version and the source's verdict`() = runTest {
        val api = mockk<OnScreenApi>()
        val json = """{"data":{"decision":"directPlay","file_id":"f1","reason":"optimized_version",
            "optimized_version":{"id":"ov","file_id":"f1","profile":"tv","label":"TV","container":"mp4",
            "video_codec":"h264","audio_codec":"aac","audio_channels":2,"stream_url":"/media/optimized/ov",
            "stream_token":"tok"},"source_decision":"transcode","source_reason":""}}"""
        val parsed = com.squareup.moshi.Moshi.Builder().build()
            .adapter<ApiResponse<PlaybackDecision>>(
                com.squareup.moshi.Types.newParameterizedType(ApiResponse::class.java, PlaybackDecision::class.java),
            ).fromJson(json)!!
        coEvery { api.playbackDecision(any(), any()) } returns parsed

        val d = TranscodeRepository(api).decide("item", "f1")!!

        assertThat(d.optimized?.stream_url).isEqualTo("/media/optimized/ov")
        assertThat(d.optimized?.stream_token).isEqualTo("tok")
        assertThat(d.source_decision).isEqualTo("transcode")
    }

    @Test
    fun `an older server's decision has no optimized version`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.playbackDecision(any(), any()) } returns ApiResponse(PlaybackDecision("transcode", "f1"))

        val d = TranscodeRepository(api).decide("item", "f1")!!

        assertThat(d.optimized).isNull()
        assertThat(d.source_decision).isNull()
    }

    @Test
    fun `too many streams on the decision throws the server's sentence`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.playbackDecision(any(), any()) } throws http(429, tooMany)

        val e = runCatching { TranscodeRepository(api).decide("item", "f1") }.exceptionOrNull()

        assertThat(e).isInstanceOf(TooManyStreamsException::class.java)
        assertThat(e?.message).isEqualTo("Your account can play one stream at a time.")
    }

    @Test
    fun `other decision failures, a plain rate limit included, read as no decision`() = runTest {
        val api = mockk<OnScreenApi>()
        val repo = TranscodeRepository(api)

        coEvery { api.playbackDecision(any(), any()) } throws http(429, """{"error":{"code":"RATE_LIMITED"}}""")
        assertThat(repo.decide("item", null)).isNull()
        coEvery { api.playbackDecision(any(), any()) } throws http(500)
        assertThat(repo.decide("item", null)).isNull()
        coEvery { api.playbackDecision(any(), any()) } throws java.io.IOException("offline")
        assertThat(repo.decide("item", null)).isNull()
    }

    @Test
    fun `too many streams on transcode start throws the server's sentence`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.startTranscode(any(), any()) } throws http(429, tooMany)

        val e = runCatching { TranscodeRepository(api).start("item", 1080, 0L) }.exceptionOrNull()

        assertThat(e).isInstanceOf(TooManyStreamsException::class.java)
    }

    @Test
    fun `other transcode start failures keep their HttpException and its body`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.startTranscode(any(), any()) } throws
            http(403, """{"error":{"code":"PARENTAL_LIMIT","message":"daily_limit_reached"}}""")

        val e = runCatching { TranscodeRepository(api).start("item", 1080, 0L) }.exceptionOrNull()

        // The 403's body is still unread: the view model parses its code.
        assertThat(e).isInstanceOf(HttpException::class.java)
        assertThat((e as HttpException).apiError()?.code).isEqualTo("PARENTAL_LIMIT")
    }

    @Test
    fun `the stream-limit sentinel round-trips the sentence, with a default for none`() {
        assertThat(StreamLimit.textOf(StreamLimit.sentinel("Stop one."))).isEqualTo("Stop one.")
        assertThat(StreamLimit.isSentinel(StreamLimit.sentinel("x"))).isTrue()
        assertThat(StreamLimit.isSentinel("playback_stopped:x")).isFalse()
        assertThat(StreamLimit.textFromServer("  ")).isEqualTo(StreamLimit.DEFAULT_TEXT)
        assertThat(StreamLimit.isRefusal(429, "TOO_MANY_SESSIONS")).isTrue()
        assertThat(StreamLimit.isRefusal(403, "TOO_MANY_SESSIONS")).isFalse()
    }
}
