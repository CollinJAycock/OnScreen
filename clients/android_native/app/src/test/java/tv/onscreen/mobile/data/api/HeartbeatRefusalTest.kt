package tv.onscreen.mobile.data.api

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.test.runTest
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.assertThrows
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response

/** The heartbeat-refusal decision shared by PlayerViewModel (foreground) and
 *  PlaybackService (background audio). Kept in step with the TV client's
 *  HeartbeatRefusalTest. */
class HeartbeatRefusalTest {

    private fun http(code: Int, body: String = "") =
        HttpException(Response.error<Unit>(code, body.toResponseBody(null)))

    private val parental =
        """{"error":{"code":"PARENTAL_LIMIT","message":"outside_allowed_hours"}}"""

    @Test
    fun `PARENTAL_LIMIT 403 on playing is a watch-limit refusal carrying the reason`() {
        assertThat(HeartbeatRefusal.of("playing", http(403, parental)))
            .isEqualTo(HeartbeatRefusal.WatchLimit("outside_allowed_hours"))
    }

    @Test
    fun `any other 403 on playing is a content-revoked refusal`() {
        // checkLibraryAccess: library grant revoked / rating ceiling lowered.
        assertThat(HeartbeatRefusal.of("playing", http(403, """{"error":{"code":"FORBIDDEN","message":"x"}}""")))
            .isEqualTo(HeartbeatRefusal.ContentRevoked)
        // No / malformed envelope still counts — the status is what matters.
        assertThat(HeartbeatRefusal.of("playing", http(403))).isEqualTo(HeartbeatRefusal.ContentRevoked)
        assertThat(HeartbeatRefusal.of("playing", http(403, "<html>nope</html>")))
            .isEqualTo(HeartbeatRefusal.ContentRevoked)
    }

    @Test
    fun `only playing reports are refusals`() {
        // The server gates only 'playing'; a refused pause/stop is not a
        // reason to stop playback.
        assertThat(HeartbeatRefusal.of("paused", http(403, parental))).isNull()
        assertThat(HeartbeatRefusal.of("stopped", http(403))).isNull()
    }

    @Test
    fun `other statuses and transport failures are not refusals`() {
        for (code in listOf(400, 401, 404, 409, 500, 502)) {
            assertThat(HeartbeatRefusal.of("playing", http(code))).isNull()
        }
        assertThat(HeartbeatRefusal.of("playing", java.io.IOException("reset"))).isNull()
        assertThat(HeartbeatRefusal.of("playing", RuntimeException("boom"))).isNull()
    }

    @Test
    fun `heartbeat returns null on success and on best-effort failures`() = runTest {
        var sent = 0
        assertThat(HeartbeatRefusal.heartbeat { sent++ }).isNull()
        assertThat(sent).isEqualTo(1)
        assertThat(HeartbeatRefusal.heartbeat { throw http(502) }).isNull()
        assertThat(HeartbeatRefusal.heartbeat { throw java.io.IOException("offline") }).isNull()
    }

    @Test
    fun `heartbeat surfaces a refusal instead of swallowing it`() = runTest {
        assertThat(HeartbeatRefusal.heartbeat { throw http(403, parental) })
            .isEqualTo(HeartbeatRefusal.WatchLimit("outside_allowed_hours"))
        assertThat(HeartbeatRefusal.heartbeat { throw http(403) })
            .isEqualTo(HeartbeatRefusal.ContentRevoked)
    }

    @Test
    fun `heartbeat lets cancellation propagate`() {
        assertThrows(CancellationException::class.java) {
            kotlinx.coroutines.runBlocking {
                HeartbeatRefusal.heartbeat { throw CancellationException("service destroyed") }
            }
        }
    }
}
