package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.mobile.data.api.HeartbeatRefusal

/** What a failed play-decision call means to the player and the cast path
 *  (TranscodeRepository.decide / decideFor). Kept in step with the TV
 *  client's TranscodeRepositoryTest. */
class PlayDecisionResultTest {
    private fun http(code: Int, body: String = "") =
        HttpException(Response.error<Any>(code, body.toResponseBody("application/json".toMediaTypeOrNull())))

    @Test
    fun `a watch limit on the decision is a refusal carrying its reason`() {
        val r = PlayDecisionResult.failure(
            http(403, """{"error":{"code":"PARENTAL_LIMIT","message":"daily_limit_reached"}}"""),
        )
        assertThat(r).isEqualTo(PlayDecisionResult.Refused(HeartbeatRefusal.WatchLimit("daily_limit_reached")))
    }

    @Test
    fun `an admin stop on the decision is a refusal`() {
        val r = PlayDecisionResult.failure(
            http(403, """{"error":{"code":"PLAYBACK_STOPPED","message":"Playback was stopped by the server admin."}}"""),
        )
        assertThat(r).isInstanceOf(PlayDecisionResult.Refused::class.java)
        assertThat((r as PlayDecisionResult.Refused).refusal).isInstanceOf(HeartbeatRefusal.PlaybackStopped::class.java)
    }

    @Test
    fun `the stream limit is still its own answer`() {
        val r = PlayDecisionResult.failure(
            http(429, """{"error":{"code":"TOO_MANY_SESSIONS","message":"Your account can play one stream at a time."}}"""),
        )
        assertThat(r).isInstanceOf(PlayDecisionResult.TooManyStreams::class.java)
    }

    @Test
    fun `anything else falls back to the local decision`() {
        // A content-rating 403: transcode Start answers it as before.
        assertThat(PlayDecisionResult.failure(http(403, """{"error":{"code":"FORBIDDEN","message":"x"}}""")))
            .isEqualTo(PlayDecisionResult.Unavailable)
        assertThat(PlayDecisionResult.failure(http(500))).isEqualTo(PlayDecisionResult.Unavailable)
        assertThat(PlayDecisionResult.failure(java.io.IOException("offline"))).isEqualTo(PlayDecisionResult.Unavailable)
    }
}
