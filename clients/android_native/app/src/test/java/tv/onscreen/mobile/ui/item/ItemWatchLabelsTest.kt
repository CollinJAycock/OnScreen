package tv.onscreen.mobile.ui.item

import com.google.common.truth.Truth.assertThat
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.i18n.UiText

/** The watch-state labels of the item page that are string resources. */
class ItemWatchLabelsTest {

    private fun season(title: String, index: Int?) = ChildItem(id = "s", title = title, type = "season", index = index)

    @Test
    fun `a season chip uses the server's title, else Specials or Season N`() {
        // A title from the server is shown as it came.
        assertThat(seasonLabel(season("Book One", 1))).isEqualTo(UiText.raw("Book One"))
        assertThat(seasonLabel(season("", 0))).isEqualTo(UiText.res(R.string.watch_specials))
        // The number fills the placeholder ("Season 3", "Temporada 3", "第 3 季").
        assertThat(seasonLabel(season(" ", 3))).isEqualTo(UiText.res(R.string.watch_season_n, 3))
        assertThat(seasonLabel(season("", null))).isEqualTo(UiText.res(R.string.watch_season))
    }

    @Test
    fun `a failed mark reads as the rate limit on 429, else a generic failure`() {
        val tooMany = HttpException(Response.error<Unit>(429, "".toResponseBody(null)))
        val broken = HttpException(Response.error<Unit>(500, "".toResponseBody(null)))
        assertThat(markErrorText(tooMany)).isEqualTo(UiText.res(R.string.watch_mark_rate_limited))
        assertThat(markErrorText(broken)).isEqualTo(UiText.res(R.string.watch_mark_failed))
        assertThat(markErrorText(RuntimeException("offline"))).isEqualTo(UiText.res(R.string.watch_mark_failed))
    }
}
