package tv.onscreen.android.ui.detail

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.android.R
import tv.onscreen.android.data.model.WatchStatusValues

class DetailStatusTest {

    @Test
    fun `offered on movies, shows, seasons, episodes, albums, artists, audiobooks and books`() {
        listOf("movie", "show", "season", "episode", "album", "artist", "audiobook", "book").forEach {
            assertThat(DetailStatus.isStatusable(it)).isTrue()
        }
        listOf("track", "photo", "podcast", "book_author", "book_series", "collection").forEach {
            assertThat(DetailStatus.isStatusable(it)).isFalse()
        }
    }

    @Test
    fun `no status reads Add to Watchlist`() {
        assertThat(DetailStatus.buttonLabel(null)).isEqualTo(R.string.status_add to null)
        // An unknown value (a newer server's status) reads as none too.
        assertThat(DetailStatus.buttonLabel("rewatching")).isEqualTo(R.string.status_add to null)
    }

    @Test
    fun `a status reads Status colon its label`() {
        assertThat(DetailStatus.buttonLabel(WatchStatusValues.PlanToWatch))
            .isEqualTo(R.string.status_label to R.string.watchlist_plan_to_watch)
        assertThat(DetailStatus.buttonLabel(WatchStatusValues.Dropped))
            .isEqualTo(R.string.status_label to R.string.watchlist_dropped)
    }

    @Test
    fun `the picker lists the five statuses, and Remove only when one is set`() {
        val none = DetailStatus.choices(null)
        assertThat(none.map { it.status }).containsExactly(
            "plan_to_watch", "watching", "on_hold", "completed", "dropped",
        ).inOrder()
        assertThat(none.map { it.labelRes }).containsExactly(
            R.string.watchlist_plan_to_watch, R.string.watchlist_watching, R.string.watchlist_on_hold,
            R.string.watchlist_completed, R.string.watchlist_dropped,
        ).inOrder()

        val set = DetailStatus.choices(WatchStatusValues.Watching)
        assertThat(set).hasSize(6)
        assertThat(set.last()).isEqualTo(DetailStatus.Choice(null, R.string.watchlist_remove))
        // An unknown value has nothing to remove that the picker can name.
        assertThat(DetailStatus.choices("rewatching")).hasSize(5)
    }

    @Test
    fun `the current status is the checked row`() {
        assertThat(DetailStatus.checkedIndex(null)).isEqualTo(-1)
        assertThat(DetailStatus.checkedIndex(WatchStatusValues.PlanToWatch)).isEqualTo(0)
        assertThat(DetailStatus.checkedIndex(WatchStatusValues.OnHold)).isEqualTo(2)
        assertThat(DetailStatus.checkedIndex(WatchStatusValues.Dropped)).isEqualTo(4)
        assertThat(DetailStatus.checkedIndex("rewatching")).isEqualTo(-1)
    }
}
