package tv.onscreen.android.ui.watchlist

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.android.data.model.WatchStatusValues
import tv.onscreen.android.data.model.WatchlistItem
import tv.onscreen.android.ui.common.WatchStateUi

class WatchlistUiTest {

    private fun item(
        type: String,
        title: String = "T",
        year: Int? = null,
        index: Int? = null,
        parentTitle: String? = null,
        parentIndex: Int? = null,
        grandparentTitle: String? = null,
        watchState: String? = null,
    ) = WatchlistItem(
        id = "i", type = type, title = title, year = year, index = index,
        parent_title = parentTitle, parent_index = parentIndex,
        grandparent_title = grandparentTitle, watch_state = watchState,
        status = WatchStatusValues.PlanToWatch,
    )

    @Test
    fun `rows run Plan to Watch, Watching, On Hold, Completed, Dropped`() {
        assertThat(WatchlistUi.STATUSES).containsExactly(
            "plan_to_watch", "watching", "on_hold", "completed", "dropped",
        ).inOrder()
        WatchlistUi.STATUSES.forEach { assertThat(WatchlistUi.labelRes(it)).isNotNull() }
        assertThat(WatchlistUi.labelRes("bogus")).isNull()
    }

    @Test
    fun `an episode reads Show, season and episode under its title`() {
        val ep = item("episode", title = "Pilot", index = 2, parentTitle = "Season 1", parentIndex = 1, grandparentTitle = "Show")
        assertThat(WatchlistUi.cardSubtitle(ep)).isEqualTo("Show · S1 · E2")
        assertThat(WatchlistUi.cardTitles(ep)).isEqualTo("Pilot" to "Show · S1 · E2")
    }

    @Test
    fun `an episode with missing numbers or show drops those parts`() {
        assertThat(WatchlistUi.cardSubtitle(item("episode", index = 0, parentIndex = 3, grandparentTitle = "Show")))
            .isEqualTo("Show · S3")
        assertThat(WatchlistUi.cardSubtitle(item("episode", index = 4, parentIndex = 1))).isEqualTo("S1 · E4")
        assertThat(WatchlistUi.cardTitles(item("episode"))).isNull()
    }

    @Test
    fun `seasons, tracks and albums use their parents`() {
        assertThat(WatchlistUi.cardTitles(item("season", title = "Season 2", parentTitle = "Show")))
            .isEqualTo("Season 2" to "Show")
        assertThat(WatchlistUi.cardSubtitle(item("track", parentTitle = "Album", grandparentTitle = "Artist")))
            .isEqualTo("Artist · Album")
        assertThat(WatchlistUi.cardSubtitle(item("album", parentTitle = "Artist", year = 1969)))
            .isEqualTo("Artist · 1969")
        assertThat(WatchlistUi.cardSubtitle(item("audiobook", year = 2001))).isEqualTo("2001")
    }

    @Test
    fun `movies and shows keep the plain two-line title`() {
        assertThat(WatchlistUi.cardTitles(item("movie", year = 1999))).isNull()
        assertThat(WatchlistUi.cardTitles(item("show", year = 2010))).isNull()
    }

    @Test
    fun `the menu offers every other status in row order`() {
        assertThat(WatchlistUi.moveTargets(WatchStatusValues.Watching))
            .containsExactly("plan_to_watch", "on_hold", "completed", "dropped").inOrder()
    }

    @Test
    fun `only a watched card gets a badge`() {
        assertThat(WatchStateUi.cardBadge(item("movie", watchState = "watched")))
            .isEqualTo(WatchStateUi.CardBadge.Watched)
        assertThat(WatchStateUi.cardBadge(item("movie", watchState = "in_progress"))).isNull()
        assertThat(WatchStateUi.cardBadge(item("movie"))).isNull()
    }

    private val rows = listOf(
        "plan_to_watch" to listOf("a", "b", "c"),
        "completed" to listOf("d", "e"),
    )

    @Test
    fun `focus returns to the same card, wherever it now sits`() {
        assertThat(WatchlistUi.focusTarget(rows, "plan_to_watch", "c", 0, 5)).isEqualTo(0 to 2)
        assertThat(WatchlistUi.focusTarget(rows, "completed", "d", 1, 3)).isEqualTo(1 to 0)
    }

    @Test
    fun `a card that left its row hands focus to its neighbour`() {
        // "b" moved away from index 1: "c" slid into its place.
        assertThat(WatchlistUi.focusTarget(rows, "plan_to_watch", "b", 0, 1)).isEqualTo(0 to 1)
        // The last card left: clamp to the new last one.
        assertThat(WatchlistUi.focusTarget(rows, "completed", "f", 1, 2)).isEqualTo(1 to 1)
    }

    @Test
    fun `a row that went away hands focus to the row now in its place`() {
        assertThat(WatchlistUi.focusTarget(rows, "watching", "x", 1, 4)).isEqualTo(1 to 0)
        assertThat(WatchlistUi.focusTarget(rows, "dropped", "x", 4, 0)).isEqualTo(1 to 0)
        assertThat(WatchlistUi.focusTarget(emptyList(), "dropped", "x", 0, 0)).isNull()
    }
}
