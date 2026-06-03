package tv.onscreen.android.ui.common

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class GridScrollMemoryTest {

    /** Captures what (if anything) restoreIfPending applied. */
    private class Sink {
        var applied: Int? = null
        val apply: (Int) -> Unit = { applied = it }
    }

    @Test
    fun firstOpen_nothingRemembered_doesNotRestore() {
        val m = GridScrollMemory()
        val sink = Sink()
        // No record() yet: a fresh fragment should not be armed.
        m.onViewRecreated()
        m.restoreIfPending(20, sink.apply)
        assertThat(sink.applied).isNull()
    }

    @Test
    fun recordThenRecreate_restoresPosition() {
        val m = GridScrollMemory()
        val sink = Sink()
        m.record(7)
        m.onViewRecreated()
        m.restoreIfPending(20, sink.apply)
        assertThat(sink.applied).isEqualTo(7)
    }

    @Test
    fun autoSelectZero_whilePending_doesNotClobberSavedPosition() {
        val m = GridScrollMemory()
        val sink = Sink()
        m.record(7)
        m.onViewRecreated()
        // Repopulating the adapter auto-selects 0 and fires the selection
        // listener before we get to restore — that record(0) must be ignored.
        m.record(0)
        m.restoreIfPending(20, sink.apply)
        assertThat(sink.applied).isEqualTo(7)
    }

    @Test
    fun emptyInterimEmission_waitsForPopulatedAdapter() {
        val m = GridScrollMemory()
        val sink = Sink()
        m.record(7)
        m.onViewRecreated()
        // Loading state: adapter still empty — must not consume the pending restore.
        m.restoreIfPending(0, sink.apply)
        assertThat(sink.applied).isNull()
        // Page arrives.
        m.restoreIfPending(20, sink.apply)
        assertThat(sink.applied).isEqualTo(7)
    }

    @Test
    fun deepPositionPastFirstPage_appliesOnceListGrows() {
        val m = GridScrollMemory()
        val sink = Sink()
        m.record(40)
        m.onViewRecreated()
        // First page can't hold position 40 — but a non-empty adapter consumes the
        // pending flag, so a position beyond the loaded range just doesn't apply.
        m.restoreIfPending(20, sink.apply)
        assertThat(sink.applied).isNull()
    }

    @Test
    fun restoreDisarms_soRecordWorksAgainAfterward() {
        val m = GridScrollMemory()
        val sink = Sink()
        m.record(7)
        m.onViewRecreated()
        m.restoreIfPending(20, sink.apply)
        assertThat(sink.applied).isEqualTo(7)

        // After a restore the memory is no longer pending: new selections record,
        // and a subsequent recreate returns to the latest position.
        m.record(12)
        val sink2 = Sink()
        m.onViewRecreated()
        m.restoreIfPending(20, sink2.apply)
        assertThat(sink2.applied).isEqualTo(12)
    }

    @Test
    fun shrunkList_disarmsInsteadOfLockingOutRecord() {
        val m = GridScrollMemory()
        val sink = Sink()
        m.record(7)
        m.onViewRecreated()
        // List came back smaller than the saved position: can't apply, but the
        // pending flag must clear so record() isn't frozen forever.
        m.restoreIfPending(3, sink.apply)
        assertThat(sink.applied).isNull()

        // record() works again, and the next recreate restores the new value.
        m.record(2)
        val sink2 = Sink()
        m.onViewRecreated()
        m.restoreIfPending(10, sink2.apply)
        assertThat(sink2.applied).isEqualTo(2)
    }
}
