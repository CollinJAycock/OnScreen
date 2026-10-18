package tv.onscreen.mobile.ui.watchlist

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.WatchStatus
import tv.onscreen.mobile.data.model.WatchlistCounts
import tv.onscreen.mobile.data.model.WatchlistItem
import tv.onscreen.mobile.data.model.WatchlistStatusCount

class WatchlistLabelsTest {

    private fun item(
        id: String,
        type: String = "movie",
        status: String = "plan_to_watch",
        year: Int? = null,
        index: Int? = null,
        parentTitle: String? = null,
        parentIndex: Int? = null,
        grandparentTitle: String? = null,
    ) = WatchlistItem(
        id = id, type = type, title = id, status = status, year = year, index = index,
        parent_title = parentTitle, parent_index = parentIndex, grandparent_title = grandparentTitle,
    )

    private val counts = WatchlistCounts(
        total = 6,
        statuses = mapOf(
            "plan_to_watch" to WatchlistStatusCount(5, mapOf("movie" to 2, "show" to 2, "episode" to 1)),
            "watching" to WatchlistStatusCount(1, mapOf("show" to 1)),
        ),
    )

    @Test
    fun `tabs run plan to watch first in tracker order`() {
        assertThat(WATCHLIST_STATUSES.map(::statusLabel))
            .containsExactly("Plan to Watch", "Watching", "On Hold", "Completed", "Dropped")
            .inOrder()
    }

    @Test
    fun `episode subtitle reads Show, S1 E2`() {
        val e = item("e", type = "episode", index = 2, parentIndex = 1, grandparentTitle = "The Show")
        assertThat(cardSubtitle(e)).isEqualTo("The Show · S1 E2")
        // Season 0 (Specials) kept; an unrecorded episode number (0) dropped.
        assertThat(cardSubtitle(e.copy(parent_index = 0, index = 0))).isEqualTo("The Show · S0")
        // No show title: just the code.
        assertThat(cardSubtitle(e.copy(grandparent_title = null))).isEqualTo("S1 E2")
    }

    @Test
    fun `other subtitles - season, track, album, movie`() {
        assertThat(cardSubtitle(item("s", type = "season", parentTitle = "The Show"))).isEqualTo("The Show")
        assertThat(cardSubtitle(item("t", type = "track", parentTitle = "Album", grandparentTitle = "Artist")))
            .isEqualTo("Artist · Album")
        assertThat(cardSubtitle(item("a", type = "album", parentTitle = "Artist", year = 1969)))
            .isEqualTo("Artist · 1969")
        assertThat(cardSubtitle(item("m", year = 1999))).isEqualTo("1999")
        assertThat(cardSubtitle(item("m"))).isEmpty()
    }

    @Test
    fun `type options are the tab's non-zero types, most common first then by label`() {
        val opts = typeOptions(counts, WatchStatus.PLAN_TO_WATCH)
        assertThat(opts.map { it.label }).containsExactly("Movies", "Shows", "Episodes").inOrder()
        assertThat(opts.first().count).isEqualTo(2)
        assertThat(typeOptions(counts, WatchStatus.DROPPED)).isEmpty()
        assertThat(typeOptions(null, WatchStatus.PLAN_TO_WATCH)).isEmpty()
        assertThat(typeLabel("some_new_type")).isEqualTo("some new type")
    }

    @Test
    fun `tab labels carry the count, zero for a missing status, bare without counts`() {
        assertThat(tabLabel(counts, WatchStatus.PLAN_TO_WATCH)).isEqualTo("Plan to Watch (5)")
        assertThat(tabLabel(counts, WatchStatus.ON_HOLD)).isEqualTo("On Hold (0)")
        assertThat(tabLabel(null, WatchStatus.ON_HOLD)).isEqualTo("On Hold")
        assertThat(tabCount(null, WatchStatus.WATCHING)).isNull()
    }

    @Test
    fun `move targets are every status but the current one`() {
        assertThat(moveTargets(item("m", status = "watching")))
            .containsExactly(WatchStatus.PLAN_TO_WATCH, WatchStatus.ON_HOLD, WatchStatus.COMPLETED, WatchStatus.DROPPED)
            .inOrder()
    }

    @Test
    fun `moving a card drops it from the page and shifts both tab counts`() {
        val m = item("m1")
        val r = applyStatusChange(listOf(m, item("m2")), counts, m, WatchStatus.WATCHING)
        assertThat(r.removed).isTrue()
        assertThat(r.items.map { it.id }).containsExactly("m2")
        assertThat(r.counts!!.total).isEqualTo(6) // moved, not removed
        assertThat(r.counts!!.statuses["plan_to_watch"]).isEqualTo(
            WatchlistStatusCount(4, mapOf("movie" to 1, "show" to 2, "episode" to 1)),
        )
        assertThat(r.counts!!.statuses["watching"]).isEqualTo(
            WatchlistStatusCount(2, mapOf("show" to 1, "movie" to 1)),
        )
    }

    @Test
    fun `removing the last of a type drops the type and the overall total`() {
        val e = item("e", type = "episode")
        val r = applyStatusChange(listOf(e), counts, e, null)
        assertThat(r.items).isEmpty()
        assertThat(r.counts!!.total).isEqualTo(5)
        assertThat(r.counts!!.statuses["plan_to_watch"]!!.types).doesNotContainKey("episode")
        assertThat(r.counts!!.statuses["plan_to_watch"]!!.total).isEqualTo(4)
    }

    @Test
    fun `same status is a no-op, and unknown counts stay unknown`() {
        val m = item("m")
        val same = applyStatusChange(listOf(m), counts, m, WatchStatus.PLAN_TO_WATCH)
        assertThat(same.removed).isFalse()
        assertThat(same.items).containsExactly(m)
        assertThat(same.counts).isSameInstanceAs(counts)

        val noCounts = applyStatusChange(listOf(m), null, m, WatchStatus.DROPPED)
        assertThat(noCounts.items).isEmpty()
        assertThat(noCounts.counts).isNull()
    }
}
