package tv.onscreen.mobile.ui.photo

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.model.PhotoTimelineBucket
import tv.onscreen.mobile.i18n.UiText

class PhotoExtrasTest {

    private fun bucket(year: Int, month: Int, count: Int = 1) =
        PhotoTimelineBucket(year = year, month = month, count = count)

    @Test
    fun `monthName returns the month's string resource for valid months`() {
        assertThat(PhotoExtras.monthName(1)).isEqualTo(R.string.photo_month_1)
        assertThat(PhotoExtras.monthName(7)).isEqualTo(R.string.photo_month_7)
        assertThat(PhotoExtras.monthName(12)).isEqualTo(R.string.photo_month_12)
    }

    @Test
    fun `monthName returns Unknown for out-of-range values`() {
        // Defensive — a malformed EXIF could set month=0 or 13.
        // Better to render "Unknown" than throw mid-list-render.
        assertThat(PhotoExtras.monthName(0)).isEqualTo(R.string.photo_month_unknown)
        assertThat(PhotoExtras.monthName(13)).isEqualTo(R.string.photo_month_unknown)
        assertThat(PhotoExtras.monthName(-3)).isEqualTo(R.string.photo_month_unknown)
        assertThat(PhotoExtras.monthName(99)).isEqualTo(R.string.photo_month_unknown)
    }

    @Test
    fun `groupByYear keeps year ordering and groups contiguously`() {
        // Server emits newest-first; assume input ordering is correct
        // and just group on year transitions (no re-sort).
        val buckets = listOf(
            bucket(2024, 12, 50),
            bucket(2024, 11, 30),
            bucket(2024, 1, 20),
            bucket(2023, 8, 100),
            bucket(2023, 1, 5),
            bucket(2020, 6, 10),
        )
        val grouped = PhotoExtras.groupByYear(buckets)
        assertThat(grouped.map { it.first }).containsExactly(2024, 2023, 2020).inOrder()
        assertThat(grouped[0].second).hasSize(3)
        assertThat(grouped[1].second).hasSize(2)
        assertThat(grouped[2].second).hasSize(1)
    }

    @Test
    fun `groupByYear handles empty input`() {
        assertThat(PhotoExtras.groupByYear(emptyList())).isEmpty()
    }

    @Test
    fun `groupByYear preserves bucket order within a year`() {
        val buckets = listOf(
            bucket(2024, 12, 5),
            bucket(2024, 11, 4),
            bucket(2024, 10, 3),
        )
        val grouped = PhotoExtras.groupByYear(buckets)
        assertThat(grouped[0].second.map { it.month }).containsExactly(12, 11, 10).inOrder()
    }

    @Test
    fun `groupByYear treats out-of-order years as new groups (no merging)`() {
        // If the server somehow emits 2024 → 2023 → 2024 (wouldn't
        // normally happen, but be explicit about the contract): we
        // produce three distinct groups, not two with the 2024s
        // merged. Single-pass algorithm — caller is responsible for
        // pre-sorting if they want merging.
        val buckets = listOf(
            bucket(2024, 12, 1),
            bucket(2023, 6, 1),
            bucket(2024, 1, 1),
        )
        val grouped = PhotoExtras.groupByYear(buckets)
        assertThat(grouped.map { it.first }).containsExactly(2024, 2023, 2024).inOrder()
    }

    @Test
    fun `photosLabel is a plural filled with the count`() {
        // The language's plural rules pick the form; the count fills %1$d.
        assertThat(PhotoExtras.photosLabel(0)).isEqualTo(UiText.Plural(R.plurals.photo_count, 0, listOf(0)))
        assertThat(PhotoExtras.photosLabel(1)).isEqualTo(UiText.Plural(R.plurals.photo_count, 1, listOf(1)))
        assertThat(PhotoExtras.photosLabel(1234)).isEqualTo(UiText.plural(R.plurals.photo_count, 1234))
    }

}
