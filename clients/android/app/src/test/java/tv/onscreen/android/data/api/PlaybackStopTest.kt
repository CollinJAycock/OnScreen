package tv.onscreen.android.data.api

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import org.junit.Test
import tv.onscreen.android.data.model.NotificationItem
import tv.onscreen.android.data.model.PlaybackStopData
import tv.onscreen.android.data.model.asPlaybackStop

/** Admin "stop this stream": event parsing, the targeting rules (kept in step
 *  with web/src/lib/playback-stop.ts isStopForPlayer) and the message. */
class PlaybackStopTest {

    private val me = "Android TV — Google Chromecast #ab12"

    private fun evt(
        item: String = "item-1",
        session: String? = null,
        client: String? = null,
        message: String? = null,
    ) = PlaybackStopData(item_id = item, session_id = session, client_name = client, message = message)

    @Test
    fun `untargeted event stops any player of that item`() {
        assertThat(PlaybackStop.targets(evt(), "item-1", null, me)).isTrue()
        assertThat(PlaybackStop.targets(evt(), "item-1", "sess-9", me)).isTrue()
    }

    @Test
    fun `a different item or nothing playing is never a target`() {
        assertThat(PlaybackStop.targets(evt(item = "item-2"), "item-1", null, me)).isFalse()
        assertThat(PlaybackStop.targets(evt(), null, null, me)).isFalse()
        assertThat(PlaybackStop.targets(evt(), "", null, me)).isFalse()
    }

    @Test
    fun `client-named event stops only the player reporting that name`() {
        assertThat(PlaybackStop.targets(evt(client = me), "item-1", null, me)).isTrue()
        assertThat(PlaybackStop.targets(evt(client = "Living Room TV"), "item-1", null, me)).isFalse()
    }

    @Test
    fun `session-scoped event matches the session, or failing that the client name`() {
        assertThat(PlaybackStop.targets(evt(session = "s1"), "item-1", "s1", me)).isTrue()
        assertThat(PlaybackStop.targets(evt(session = "s1"), "item-1", "s2", me)).isFalse()
        assertThat(PlaybackStop.targets(evt(session = "s1"), "item-1", null, me)).isFalse()
        // Direct play has no session of its own, but the name still matches.
        assertThat(PlaybackStop.targets(evt(session = "s1", client = me), "item-1", null, me)).isTrue()
        assertThat(PlaybackStop.targets(evt(session = "s1", client = "Other"), "item-1", "s2", me)).isFalse()
    }

    @Test
    fun `message wording matches the server's 403 sentence`() {
        assertThat(PlaybackStop.text("bedtime")).isEqualTo("Playback was stopped by the server admin: bedtime")
        assertThat(PlaybackStop.text("  ")).isEqualTo("Playback was stopped by the server admin.")
        assertThat(PlaybackStop.text(null)).isEqualTo("Playback was stopped by the server admin.")
        assertThat(PlaybackStop.textFromServer("Playback was stopped by the server admin: x"))
            .isEqualTo("Playback was stopped by the server admin: x")
        assertThat(PlaybackStop.textFromServer("")).isEqualTo("Playback was stopped by the server admin.")
    }

    @Test
    fun `sentinel round-trips the sentence`() {
        val s = PlaybackStop.sentinel(PlaybackStop.text("bye"))
        assertThat(PlaybackStop.isSentinel(s)).isTrue()
        assertThat(PlaybackStop.sentenceOf(s)).isEqualTo("Playback was stopped by the server admin: bye")
        assertThat(PlaybackStop.sentenceOf(PlaybackStop.SENTINEL_PREFIX))
            .isEqualTo("Playback was stopped by the server admin.")
        for (other in listOf(null, "content_restricted", "watch_limit:x", "Playback error")) {
            assertThat(PlaybackStop.isSentinel(other)).isFalse()
        }
    }

    @Test
    fun `playback stop event parses from the SSE json`() {
        val adapter = Moshi.Builder().build().adapter(NotificationItem::class.java)
        val item = adapter.fromJson(
            """{"type":"playback.stop","item_id":"item-1","created_at":1,
               "data":{"item_id":"item-1","session_id":"s1","client_name":"TV","decision":"remux","message":"bye"}}""",
        )!!
        assertThat(item.asPlaybackStop()).isEqualTo(
            PlaybackStopData(item_id = "item-1", session_id = "s1", client_name = "TV", decision = "remux", message = "bye"),
        )
    }

    @Test
    fun `blank optional fields read as absent and a missing item id is ignored`() {
        val blank = NotificationItem(
            type = "playback.stop",
            data = mapOf("item_id" to "item-1", "session_id" to "", "client_name" to "", "message" to ""),
        )
        assertThat(blank.asPlaybackStop()).isEqualTo(PlaybackStopData(item_id = "item-1"))
        assertThat(NotificationItem(type = "playback.stop", data = mapOf("message" to "x")).asPlaybackStop()).isNull()
        assertThat(NotificationItem(type = "playback.stop", data = mapOf("item_id" to "")).asPlaybackStop()).isNull()
        assertThat(NotificationItem(type = "playback.stop").asPlaybackStop()).isNull()
    }
}
