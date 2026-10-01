package tv.onscreen.android.playback

import com.google.common.truth.Truth.assertThat
import org.junit.After
import org.junit.Test
import tv.onscreen.android.data.model.TranscodeSession

class StreamSessionTest {

    @After
    fun tearDown() = StreamTokenVault.clear()

    @Test
    fun `an opened session keeps its playlist clean and its token vaulted`() {
        val s = StreamSession.opened(
            TranscodeSession(
                session_id = "sess-1",
                playlist_url = "/api/v1/transcode/sessions/sess-1/playlist.m3u8?token=sess-tok",
                token = "sess-tok",
                start_offset_sec = 12.5,
            ),
            serverUrl = "http://srv",
            requestedMs = 15_000L,
        )
        assertThat(s.id).isEqualTo("sess-1")
        assertThat(s.token).isEqualTo("sess-tok")
        // The server's keyframe-aligned opening, not the position asked for.
        assertThat(s.offsetMs).isEqualTo(12_500L)
        assertThat(s.playlistUrl).isEqualTo("http://srv/api/v1/transcode/sessions/sess-1/playlist.m3u8")
        assertThat(StreamTokenVault.tokenForTest(s.playlistUrl)).isEqualTo("sess-tok")
    }

    @Test
    fun `a server that omits the opening offset opens where it was asked`() {
        val s = StreamSession.opened(
            TranscodeSession(session_id = "s", playlist_url = "/p.m3u8", token = "t"),
            serverUrl = "http://srv",
            requestedMs = 15_000L,
        )
        assertThat(s.offsetMs).isEqualTo(15_000L)
    }

    @Test
    fun `a session opening at 0 opens at 0, not where it was asked`() {
        // A stream covering the whole file (a pre-encoded ladder), or a
        // resume before the first keyframe after 0:00. Read as "not sent",
        // the offset went to the resume point over a stream starting at 0:00.
        val s = StreamSession.opened(
            TranscodeSession(session_id = "s", playlist_url = "/p.m3u8", token = "t", start_offset_sec = 0.0),
            serverUrl = "http://srv",
            requestedMs = 2_700_000L,
        )
        assertThat(s.offsetMs).isEqualTo(0L)
    }
}
