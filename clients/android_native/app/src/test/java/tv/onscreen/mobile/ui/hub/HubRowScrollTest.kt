package tv.onscreen.mobile.ui.hub

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class HubRowScrollTest {

    @Test
    fun `a new head scrolls the row back to the start`() {
        // Just played B: the refresh moves it in front of A.
        assertThat(rowHeadChanged(previousHeadId = "A", headId = "B")).isTrue()
    }

    @Test
    fun `an unchanged head leaves a scrolled row alone`() {
        assertThat(rowHeadChanged("A", "A")).isFalse()
    }

    @Test
    fun `a row appearing or emptying is not a change`() {
        assertThat(rowHeadChanged(null, "A")).isFalse()
        assertThat(rowHeadChanged("A", null)).isFalse()
        assertThat(rowHeadChanged(null, null)).isFalse()
    }
}
