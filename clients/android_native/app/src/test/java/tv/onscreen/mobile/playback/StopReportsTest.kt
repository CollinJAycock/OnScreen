package tv.onscreen.mobile.playback

import androidx.media3.common.Player
import com.google.common.truth.Truth.assertThat
import org.junit.Test

class StopReportsTest {

    private fun reports(reason: Int, state: Int = Player.STATE_READY, left: String? = "a", new: String? = "b") =
        StopReports.onDiscontinuity(reason, state, left, new)

    @Test
    fun `gapless hand-off and skips report the track left behind`() {
        assertThat(reports(Player.DISCONTINUITY_REASON_AUTO_TRANSITION)).isTrue()
        assertThat(reports(Player.DISCONTINUITY_REASON_SEEK)).isTrue()
        assertThat(reports(Player.DISCONTINUITY_REASON_SKIP)).isTrue()
    }

    @Test
    fun `seeking within a track or restarting it is not a stop`() {
        assertThat(reports(Player.DISCONTINUITY_REASON_SEEK, left = "a", new = "a")).isFalse()
        assertThat(reports(Player.DISCONTINUITY_REASON_AUTO_TRANSITION, left = "a", new = "a")).isFalse()
    }

    @Test
    fun `replacing the queue mid-track reports, clearing a stopped player does not`() {
        // The UI hands over another album while this one plays.
        assertThat(reports(Player.DISCONTINUITY_REASON_REMOVE, state = Player.STATE_BUFFERING)).isTrue()
        // ✕ / refusal / admin stop / sign-out: stop() then clear — already
        // reported (or deliberately not) through STATE_IDLE.
        assertThat(reports(Player.DISCONTINUITY_REASON_REMOVE, state = Player.STATE_IDLE, new = null)).isFalse()
    }

    @Test
    fun `other discontinuities and unknown items never report`() {
        assertThat(reports(Player.DISCONTINUITY_REASON_INTERNAL)).isFalse()
        assertThat(reports(Player.DISCONTINUITY_REASON_SEEK_ADJUSTMENT)).isFalse()
        assertThat(reports(Player.DISCONTINUITY_REASON_AUTO_TRANSITION, left = null)).isFalse()
        assertThat(reports(Player.DISCONTINUITY_REASON_AUTO_TRANSITION, left = "")).isFalse()
    }

    @Test
    fun `an admin stop acts once per playback however many ways it arrives`() {
        val dedupe = AdminStopDedupe()
        assertThat(dedupe.first("t1")).isTrue() // SSE event
        assertThat(dedupe.first("t1")).isFalse() // heartbeat 403
        assertThat(dedupe.first("t1")).isFalse() // media 403
        assertThat(dedupe.first("t2")).isTrue() // a different track
    }

    @Test
    fun `replaying the same track right away is a new playback whose refusal halts it`() {
        val dedupe = AdminStopDedupe()
        assertThat(dedupe.first("t1")).isTrue() // admin stop: halted, queue cleared
        // The user taps the track again within seconds (still inside the
        // server's stop window): it becomes current again…
        dedupe.reset()
        // …and its media 403 PLAYBACK_STOPPED must halt + explain again, not
        // be swallowed (the old 15 s window did, leaving the errored track in
        // the mini player).
        assertThat(dedupe.first("t1")).isTrue()
        assertThat(dedupe.first("t1")).isFalse()
    }

    @Test
    fun `the stop subscription is held only while actually playing`() {
        // Playing / buffering to play.
        assertThat(wantsAdminStopEvents(true, Player.STATE_READY, hasItem = true)).isTrue()
        assertThat(wantsAdminStopEvents(true, Player.STATE_BUFFERING, hasItem = true)).isTrue()
        // Paused in the background — the old rule (any current item) kept the
        // socket and its 30 s keepalive wakeups alive for hours here.
        assertThat(wantsAdminStopEvents(false, Player.STATE_READY, hasItem = true)).isFalse()
        assertThat(wantsAdminStopEvents(false, Player.STATE_BUFFERING, hasItem = true)).isFalse()
        // Ended (queue ran out) / errored or stopped (IDLE) / nothing queued.
        assertThat(wantsAdminStopEvents(true, Player.STATE_ENDED, hasItem = true)).isFalse()
        assertThat(wantsAdminStopEvents(true, Player.STATE_IDLE, hasItem = true)).isFalse()
        assertThat(wantsAdminStopEvents(true, Player.STATE_READY, hasItem = false)).isFalse()
    }
}
