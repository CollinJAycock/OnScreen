package tv.onscreen.mobile.ui.downloads

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.downloads.DownloadEntry
import tv.onscreen.mobile.i18n.UiText

/** The Downloads list's text: a plural count and the status placeholders. */
class DownloadLabelsTest {

    private fun entry(status: String, size: Long = 0, done: Long = 0, error: String? = null, progress: Double = 0.0) =
        DownloadEntry(
            file_id = "f", item_id = "i", item_title = "Film", item_type = "movie", container = "mp4",
            size_bytes = size, downloaded_bytes = done, status = status, error = error, convert_progress = progress,
        )

    @Test
    fun `the storage line is a plural of the item count`() {
        assertThat(storageSummary(1, 512)).isEqualTo(UiText.plural(R.plurals.downloads_storage_summary, 1, 1, "512B"))
        assertThat(storageSummary(3, 512)).isEqualTo(UiText.plural(R.plurals.downloads_storage_summary, 3, 3, "512B"))
        // The count picks the form and also fills %1$d; the size fills %2$s.
        val p = storageSummary(0, 0) as UiText.Plural
        assertThat(p.count).isEqualTo(0)
        assertThat(p.args).containsExactly(0, "0B").inOrder()
    }

    @Test
    fun `each status fills its own placeholders`() {
        assertThat(downloadStatus(entry("completed", size = 100)))
            .isEqualTo(UiText.res(R.string.downloads_status_completed, "100B"))
        assertThat(downloadStatus(entry("downloading", size = 200, done = 50)))
            .isEqualTo(UiText.res(R.string.downloads_status_downloading, 25, "50B", "200B"))
        assertThat(downloadStatus(entry("converting", progress = 0.4)))
            .isEqualTo(UiText.res(R.string.downloads_status_converting, 40))
        assertThat(downloadStatus(entry("queued"))).isEqualTo(UiText.res(R.string.downloads_status_queued))
    }

    @Test
    fun `a failure shows the recorded reason, else the app's word`() {
        assertThat(downloadStatus(entry("failed", error = "HTTP 500"))).isEqualTo(UiText.raw("HTTP 500"))
        assertThat(downloadStatus(entry("failed"))).isEqualTo(UiText.res(R.string.downloads_status_failed))
        // An unknown status reads as sent.
        assertThat(downloadStatus(entry("paused"))).isEqualTo(UiText.raw("paused"))
    }
}
