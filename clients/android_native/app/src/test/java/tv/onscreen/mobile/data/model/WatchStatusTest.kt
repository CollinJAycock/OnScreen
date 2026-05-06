package tv.onscreen.mobile.data.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Pure tests for the [WatchStatus] wire format. The five enum values
 * map to lowercase snake_case strings and a tolerant fromWire parser
 * — verify both directions plus the unknown-input fallback that lets
 * UI treat "no status" as a first-class state.
 */
class WatchStatusTest {

    @Test
    fun `each enum value carries the expected wire string`() {
        assertThat(WatchStatus.PLAN_TO_WATCH.wire).isEqualTo("plan_to_watch")
        assertThat(WatchStatus.WATCHING.wire).isEqualTo("watching")
        assertThat(WatchStatus.ON_HOLD.wire).isEqualTo("on_hold")
        assertThat(WatchStatus.COMPLETED.wire).isEqualTo("completed")
        assertThat(WatchStatus.DROPPED.wire).isEqualTo("dropped")
    }

    @Test
    fun `fromWire round-trips every value`() {
        for (s in WatchStatus.values()) {
            assertThat(WatchStatus.fromWire(s.wire)).isEqualTo(s)
        }
    }

    @Test
    fun `fromWire returns null for unknown or null input`() {
        // Server adding a sixth status (e.g. "rewatching") shouldn't
        // crash the client — we surface unknowns as "no row" so the
        // UI falls back to the no-status state.
        assertThat(WatchStatus.fromWire(null)).isNull()
        assertThat(WatchStatus.fromWire("")).isNull()
        assertThat(WatchStatus.fromWire("rewatching")).isNull()
    }

    @Test
    fun `fromWire is case-sensitive (matches server contract)`() {
        // Server emits lowercase; uppercased input is invalid input.
        // Strict matching catches accidental UPPER_SNAKE bleed-through
        // from someone passing the enum's name() instead of .wire.
        assertThat(WatchStatus.fromWire("WATCHING")).isNull()
        assertThat(WatchStatus.fromWire("Plan_To_Watch")).isNull()
    }
}
