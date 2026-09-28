package tv.onscreen.mobile.data.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Targeting + wording of the admin "stop this stream" event. Mirrors the web
 *  client's playback-stop.ts rules (isStopForPlayer / adminStopText). */
class PlaybackStopEventTest {

    private fun ev(
        item: String = "movie-1",
        session: String? = null,
        client: String? = null,
        message: String? = null,
    ) = PlaybackStopEvent(item_id = item, session_id = session, client_name = client, message = message)

    @Test
    fun `untargeted stop hits any player of that item`() {
        assertThat(ev().targets("movie-1", sessionId = null, clientName = null)).isTrue()
        assertThat(ev().targets("movie-1", sessionId = "sess-9", clientName = null)).isTrue()
    }

    @Test
    fun `a stop for another item, or with nothing loaded, never matches`() {
        assertThat(ev(item = "other").targets("movie-1", null, null)).isFalse()
        assertThat(ev().targets(null, null, null)).isFalse()
        assertThat(ev().targets("", null, null)).isFalse()
    }

    @Test
    fun `session-targeted stop hits only the owning session`() {
        assertThat(ev(session = "sess-1").targets("movie-1", "sess-1", null)).isTrue()
        assertThat(ev(session = "sess-1").targets("movie-1", "sess-2", null)).isFalse()
        // A direct-playing phone has no session: a stop aimed at someone
        // else's transcode of the same item must not stop it.
        assertThat(ev(session = "sess-1").targets("movie-1", null, null)).isFalse()
    }

    @Test
    fun `session-targeted stop still hits the player whose client name matches`() {
        assertThat(ev(session = "sess-1", client = "Pixel").targets("movie-1", "sess-2", "Pixel")).isTrue()
    }

    @Test
    fun `name-targeted stop hits only the named client`() {
        assertThat(ev(client = "Living Room TV").targets("movie-1", null, "Living Room TV")).isTrue()
        assertThat(ev(client = "Living Room TV").targets("movie-1", null, null)).isFalse()
        assertThat(ev(client = "Living Room TV").targets("movie-1", null, "Pixel")).isFalse()
    }

    @Test
    fun `display text uses the server wording`() {
        assertThat(ev(message = "  server maintenance ").displayText)
            .isEqualTo("Playback was stopped by the server admin: server maintenance")
        assertThat(ev().displayText).isEqualTo("Playback was stopped by the server admin.")
        assertThat(PlaybackStop.textFromServer(null)).isEqualTo("Playback was stopped by the server admin.")
        assertThat(PlaybackStop.textFromServer(" Playback was stopped by the server admin: x "))
            .isEqualTo("Playback was stopped by the server admin: x")
    }

    @Test
    fun `asPlaybackStop reads the payload and treats empty strings as absent`() {
        val n = NotificationItem(
            type = "playback.stop",
            data = mapOf(
                "item_id" to "movie-1",
                "session_id" to "",
                "client_name" to "Pixel",
                "decision" to "directPlay",
                "message" to "bye",
            ),
        )
        assertThat(n.asPlaybackStop()).isEqualTo(
            PlaybackStopEvent(item_id = "movie-1", client_name = "Pixel", decision = "directPlay", message = "bye"),
        )
    }

    @Test
    fun `asPlaybackStop rejects a payload without an item id`() {
        assertThat(NotificationItem(type = "playback.stop").asPlaybackStop()).isNull()
        assertThat(NotificationItem(type = "playback.stop", data = mapOf("item_id" to "")).asPlaybackStop()).isNull()
        assertThat(NotificationItem(type = "playback.stop", data = mapOf("item_id" to 7.0)).asPlaybackStop()).isNull()
    }

    @Test
    fun `asProgressUpdate reads JSON numbers as longs`() {
        val n = NotificationItem(
            type = "progress.updated",
            data = mapOf("item_id" to "m", "position_ms" to 60000.0, "duration_ms" to 600000.0, "state" to "playing"),
        )
        assertThat(n.asProgressUpdate()).isEqualTo(ProgressUpdateData("m", 60_000L, 600_000L, "playing"))
        assertThat(NotificationItem(type = "progress.updated", data = mapOf("item_id" to "m")).asProgressUpdate()).isNull()
    }

    @Test
    fun `request notification types are hidden`() {
        listOf(
            "request_created", "request_pending", "request_approved", "request_declined",
            "request_available", "request_failed", "request_season_available", "request.updated",
        ).forEach { assertThat(isHiddenNotificationType(it)).isTrue() }
        listOf("new_content", "scan_complete", "issue_resolved", "progress.updated", "playback.stop", "system")
            .forEach { assertThat(isHiddenNotificationType(it)).isFalse() }
    }
}
