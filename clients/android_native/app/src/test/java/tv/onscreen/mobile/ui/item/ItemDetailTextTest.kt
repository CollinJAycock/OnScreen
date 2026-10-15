package tv.onscreen.mobile.ui.item

import android.net.Uri
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockkStatic
import io.mockk.unmockkStatic
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.ItemStudio

class ItemDetailTextTest {

    // android.net.Uri is a stub in JVM tests: let path segments through.
    @Before fun setUp() {
        mockkStatic(Uri::class)
        every { Uri.encode(any()) } answers { firstArg() }
    }

    @After fun tearDown() { unmockkStatic(Uri::class) }

    private fun item(type: String, logo: String?) =
        ItemDetail(id = "i", library_id = "l", title = "Heat", type = type, logo_path = logo)

    @Test
    fun `the meta line is the year, then the studios`() {
        val studios = listOf(ItemStudio(id = "1", name = "Warner Bros."), ItemStudio(id = "2", name = "Regency"))
        assertThat(detailMetaLine(1995, studios)).isEqualTo("1995 · Warner Bros. · Regency")
        assertThat(detailMetaLine(null, studios)).isEqualTo("Warner Bros. · Regency")
        assertThat(detailMetaLine(1995, emptyList())).isEqualTo("1995")
        assertThat(detailMetaLine(null, emptyList())).isNull()
        // Blank or repeated names add nothing.
        assertThat(detailMetaLine(1995, listOf(ItemStudio(name = " "), ItemStudio(name = "HBO"), ItemStudio(name = "HBO"))))
            .isEqualTo("1995 · HBO")
    }

    @Test
    fun `the logo is fetched at its own size, never resized to a JPEG`() {
        assertThat(detailLogoUrl("http://srv", item("movie", "items/i/logo.png")))
            .isEqualTo("http://srv/artwork/items/i/logo.png")
        assertThat(detailLogoUrl("http://srv", item("show", "s/logo.png"))).doesNotContain("?w=")
    }

    @Test
    fun `no logo, another type or no server means the text title`() {
        assertThat(detailLogoUrl("http://srv", item("movie", null))).isNull()
        assertThat(detailLogoUrl("http://srv", item("movie", " "))).isNull()
        // A season's or episode's title isn't the show's name.
        assertThat(detailLogoUrl("http://srv", item("season", "s/logo.png"))).isNull()
        assertThat(detailLogoUrl("http://srv", item("episode", "s/logo.png"))).isNull()
        assertThat(detailLogoUrl("", item("movie", "logo.png"))).isNull()
    }
}
