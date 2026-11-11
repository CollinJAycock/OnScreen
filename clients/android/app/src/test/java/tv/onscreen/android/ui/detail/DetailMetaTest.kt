package tv.onscreen.android.ui.detail

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import org.junit.Test
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.model.StudioRef
import tv.onscreen.android.i18n.AppLanguage
import tv.onscreen.android.i18n.TestStrings

/** The detail screen's metadata line and clear logo (server v2.6). */
class DetailMetaTest {

    private fun line(item: ItemDetail, tag: String = AppLanguage.ENGLISH) =
        DetailMeta.lineParts(item).map { TestStrings.resolve(it, tag) }

    @Test
    fun `the running time reads as before in English, in each language's words elsewhere`() {
        val movie = item().copy(duration_ms = 90 * 60_000L, content_rating = "PG-13")
        assertThat(line(movie)).containsExactly("1999", "PG-13", "1h 30m", "Drama").inOrder()
        assertThat(line(movie, AppLanguage.SPANISH)).contains("1 h 30 min")
        assertThat(line(movie, AppLanguage.CHINESE)).contains("1 小时 30 分钟")
        assertThat(TestStrings.resolve(DetailMeta.runtime(45 * 60_000L))).isEqualTo("45m")
        assertThat(TestStrings.resolve(DetailMeta.runtime(45 * 60_000L), AppLanguage.SPANISH)).isEqualTo("45 min")
    }

    private fun item(type: String = "movie", logo: String? = null, studios: List<StudioRef> = emptyList()) =
        ItemDetail(
            id = "i", library_id = "l", title = "T", type = type, year = 1999,
            genres = listOf("Drama"), logo_path = logo, studios = studios,
        )

    @Test
    fun `studios and networks join the metadata line after the genres`() {
        val parts = line(
            item(studios = listOf(StudioRef("1", "Warner Bros."), StudioRef("2", "Village Roadshow"), StudioRef("3", "Silver"))),
        )

        assertThat(parts).containsExactly("1999", "Drama", "Warner Bros., Village Roadshow").inOrder()
    }

    @Test
    fun `no studios leaves the line as it was`() {
        assertThat(line(item())).containsExactly("1999", "Drama").inOrder()
        assertThat(line(item(studios = listOf(StudioRef("1", " "))))).containsExactly("1999", "Drama")
    }

    @Test
    fun `a movie's or show's logo stands in for the title, nothing else's`() {
        assertThat(DetailMeta.logoPath(item(logo = "a/logo.png"))).isEqualTo("a/logo.png")
        assertThat(DetailMeta.logoPath(item(type = "show", logo = "s/logo.png"))).isEqualTo("s/logo.png")
        assertThat(DetailMeta.logoPath(item(type = "episode", logo = "e/logo.png"))).isNull()
        assertThat(DetailMeta.logoPath(item(logo = ""))).isNull()
        assertThat(DetailMeta.logoPath(item())).isNull()
    }

    @Test
    fun `detail decodes logo, banner, disc art and studios, and older servers' lack of them`() {
        val adapter = Moshi.Builder().build().adapter(ItemDetail::class.java)
        val v26 = adapter.fromJson(
            """{"id":"i","library_id":"l","title":"T","type":"movie","logo_path":"x/logo.png",
               "banner_path":"x/banner.jpg","disc_art_path":"x/disc.png",
               "studios":[{"id":"s1","name":"A24","kind":"studio"}]}""",
        )!!
        val old = adapter.fromJson("""{"id":"i","library_id":"l","title":"T","type":"movie"}""")!!

        assertThat(v26.logo_path).isEqualTo("x/logo.png")
        assertThat(v26.banner_path).isEqualTo("x/banner.jpg")
        assertThat(v26.disc_art_path).isEqualTo("x/disc.png")
        assertThat(v26.studios.map { it.name }).containsExactly("A24")
        assertThat(old.logo_path).isNull()
        assertThat(old.studios).isEmpty()
    }
}
