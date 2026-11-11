package tv.onscreen.mobile.i18n

import android.content.res.Resources
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import org.junit.Test
import tv.onscreen.mobile.R

class UiTextTest {

    private val res = mockk<Resources>()

    @Test
    fun `a resource without arguments is looked up as is`() {
        every { res.getString(R.string.retry) } returns "Reintentar"
        assertThat(UiText.res(R.string.retry).asString(res)).isEqualTo("Reintentar")
    }

    @Test
    fun `placeholders are filled in order`() {
        every { res.getString(R.string.bookmark_note_count, 12, 500) } returns "12 / 500"
        assertThat(UiText.res(R.string.bookmark_note_count, 12, 500).asString(res)).isEqualTo("12 / 500")
    }

    @Test
    fun `a plural passes the count to pick the form and to fill it`() {
        every { res.getQuantityString(42, 3, 3) } returns "3 items"
        assertThat(UiText.plural(42, 3).asString(res)).isEqualTo("3 items")
        verify { res.getQuantityString(42, 3, 3) }
    }

    @Test
    fun `a plural can fill other arguments than the count`() {
        every { res.getQuantityString(42, 1, "Jazz", 1) } returns "Jazz: 1 track"
        assertThat(UiText.plural(42, 1, "Jazz", 1).asString(res)).isEqualTo("Jazz: 1 track")
    }

    @Test
    fun `nested text is resolved before it fills a placeholder`() {
        every { res.getString(R.string.retry) } returns "重试"
        every { res.getString(R.string.player_speed_button, "重试") } returns "播放速度，重试"
        val text = UiText.res(R.string.player_speed_button, UiText.res(R.string.retry))
        assertThat(text.asString(res)).isEqualTo("播放速度，重试")
    }

    @Test
    fun `raw text is never translated`() {
        assertThat(UiText.raw("Not found").asString(res)).isEqualTo("Not found")
    }

    @Test
    fun `texts compare by value, so view models are tested without resources`() {
        assertThat(UiText.res(R.string.bookmark_chapter_n, 3)).isEqualTo(UiText.res(R.string.bookmark_chapter_n, 3))
        assertThat(UiText.res(R.string.bookmark_chapter_n, 3)).isNotEqualTo(UiText.res(R.string.bookmark_chapter_n, 4))
        assertThat(UiText.plural(7, 2)).isEqualTo(UiText.Plural(7, 2, listOf(2)))
    }
}
