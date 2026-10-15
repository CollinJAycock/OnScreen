package tv.onscreen.mobile.data.downloads

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.downloads.DownloadChoices.Options
import tv.onscreen.mobile.data.downloads.DownloadChoices.Plan
import tv.onscreen.mobile.data.model.DownloadOptions
import tv.onscreen.mobile.data.model.DownloadQuality

/** What Download offers, v2.6 converted downloads. */
class DownloadChoicesTest {

    private val qualities = listOf(
        DownloadQuality(id = "1080p_8mbps", label = "1080p · 8 Mbps", bitrate_kbps = 8000, max_height = 1080, allowed = false),
        DownloadQuality(id = "720p_4mbps", label = "720p · 4 Mbps", bitrate_kbps = 4000, max_height = 720, allowed = true),
    )
    private val offered = Options.Offered(DownloadOptions(file_id = "f1", qualities = qualities))

    @Test
    fun `the original comes first, then the smaller copies`() {
        val plan = DownloadChoices.plan(offered, stacked = false) as Plan.Choose
        assertThat(plan.choices.map { it.quality })
            .containsExactly(DownloadChoices.ORIGINAL, "1080p_8mbps", "720p_4mbps").inOrder()
        assertThat(plan.choices.first().label).isEqualTo("Original")
    }

    @Test
    fun `a copy above the streaming limit is listed but can't be picked`() {
        val plan = DownloadChoices.plan(offered, stacked = false) as Plan.Choose
        val above = plan.choices.single { it.quality == "1080p_8mbps" }
        assertThat(above.enabled).isFalse()
        assertThat(above.note).isEqualTo(DownloadChoices.ABOVE_LIMIT_NOTE)
        assertThat(plan.choices.single { it.quality == "720p_4mbps" }.enabled).isTrue()
    }

    @Test
    fun `an older server or an unconvertible file downloads the original at once`() {
        assertThat(DownloadChoices.plan(Options.Unavailable, stacked = false)).isEqualTo(Plan.Original)
        assertThat(DownloadChoices.plan(Options.Offered(DownloadOptions(qualities = emptyList())), stacked = false))
            .isEqualTo(Plan.Original)
    }

    @Test
    fun `downloads turned off on the server are refused with a clear message`() {
        assertThat(DownloadChoices.plan(Options.Disabled, stacked = false))
            .isEqualTo(Plan.Blocked(DownloadChoices.DISABLED_MESSAGE))
        assertThat(DownloadChoices.plan(Options.Disabled, stacked = true))
            .isEqualTo(Plan.Blocked(DownloadChoices.DISABLED_MESSAGE))
    }

    @Test
    fun `a stacked movie offers only a converted copy of the whole movie`() {
        // Its first file is only part 1: the original is never offered.
        val plan = DownloadChoices.plan(offered, stacked = true) as Plan.Choose
        assertThat(plan.choices.map { it.quality }).containsExactly("1080p_8mbps", "720p_4mbps").inOrder()
    }

    @Test
    fun `a stacked movie with nothing convertible says why instead of fetching part 1`() {
        assertThat(DownloadChoices.plan(Options.Unavailable, stacked = true))
            .isEqualTo(Plan.Blocked(DownloadChoices.STACKED_MESSAGE))
        assertThat(DownloadChoices.plan(Options.Offered(DownloadOptions()), stacked = true))
            .isEqualTo(Plan.Blocked(DownloadChoices.STACKED_MESSAGE))
        val allAbove = Options.Offered(DownloadOptions(qualities = qualities.map { it.copy(allowed = false) }))
        assertThat(DownloadChoices.plan(allAbove, stacked = true))
            .isEqualTo(Plan.Blocked(DownloadChoices.STACKED_ABOVE_LIMIT_MESSAGE))
    }
}
