package tv.onscreen.android.ui.playback

import android.graphics.Bitmap
import androidx.leanback.widget.PlaybackSeekDataProvider
import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.every
import io.mockk.mockk
import io.mockk.mockkStatic
import io.mockk.unmockkStatic
import io.mockk.verify
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.android.data.model.TrickplayCue
import tv.onscreen.android.data.repository.TrickplayRepository

/** Sprite sheets are fetched once at a time each, and only a couple are
 *  kept decoded. */
@OptIn(ExperimentalCoroutinesApi::class)
class TrickplaySeekProviderTest {

    private val tile = mockk<Bitmap>(relaxed = true)

    @Before
    fun setUp() {
        mockkStatic(Bitmap::class)
        every { Bitmap.createBitmap(any<Bitmap>(), any<Int>(), any<Int>(), any<Int>(), any<Int>()) } returns tile
    }

    @After
    fun tearDown() {
        unmockkStatic(Bitmap::class)
    }

    private fun sheet(): Bitmap = mockk(relaxed = true) {
        every { width } returns 3200
        every { height } returns 1800
        every { isRecycled } returns false
    }

    /** Ten 320x180 cues per sheet, row 0 of sheets s0, s1, s2... */
    private fun cues(sheets: Int) = (0 until sheets * 10).map { i ->
        TrickplayCue(
            startMs = i * 10_000L, endMs = (i + 1) * 10_000L,
            file = "s${i / 10}.jpg", x = (i % 10) * 320, y = 0, w = 320, h = 180,
        )
    }

    private class Loaded : PlaybackSeekDataProvider.ResultCallback() {
        val indexes = mutableListOf<Int>()
        override fun onThumbnailLoaded(bitmap: Bitmap?, index: Int) {
            indexes += index
        }
    }

    @Test
    fun `neighbouring slots on one sheet share a single download`() {
        val scope = TestScope()
        val repo = mockk<TrickplayRepository>()
        coEvery { repo.fetchSprite("item", "s0.jpg") } coAnswers { delay(500); sheet() }
        val provider = TrickplaySeekProvider("item", cues(1), repo, scope, hlsOffsetMs = 0L)
        val loaded = Loaded()

        // Leanback asks for the slots around the scrub position together.
        (0..6).forEach { provider.getThumbnail(it, loaded) }
        scope.advanceUntilIdle()

        coVerify(exactly = 1) { repo.fetchSprite("item", "s0.jpg") }
        assertThat(loaded.indexes).containsExactlyElementsIn(0..6)
    }

    @Test
    fun `a cancelled slot doesn't abort the sheet the others wait for`() {
        val scope = TestScope()
        val repo = mockk<TrickplayRepository>()
        coEvery { repo.fetchSprite("item", "s0.jpg") } coAnswers { delay(500); sheet() }
        val provider = TrickplaySeekProvider("item", cues(1), repo, scope, hlsOffsetMs = 0L)
        val loaded = Loaded()

        provider.getThumbnail(0, loaded)
        provider.getThumbnail(1, loaded)
        // Slot 0 asked again (fast scrub): its first request is cancelled.
        provider.getThumbnail(0, loaded)
        scope.advanceUntilIdle()

        coVerify(exactly = 1) { repo.fetchSprite("item", "s0.jpg") }
        assertThat(loaded.indexes).containsExactly(0, 1)
    }

    @Test
    fun `only the two most recent sheets stay decoded`() {
        val scope = TestScope()
        val repo = mockk<TrickplayRepository>()
        val sheets = (0 until 3).associate { "s$it.jpg" to sheet() }
        sheets.forEach { (file, bmp) -> coEvery { repo.fetchSprite("item", file) } returns bmp }
        val provider = TrickplaySeekProvider("item", cues(3), repo, scope, hlsOffsetMs = 0L)
        val loaded = Loaded()

        // A scrub through three sheets.
        provider.getThumbnail(0, loaded)
        scope.advanceUntilIdle()
        provider.getThumbnail(10, loaded)
        scope.advanceUntilIdle()
        provider.getThumbnail(20, loaded)
        scope.advanceUntilIdle()

        verify(exactly = 1) { sheets.getValue("s0.jpg").recycle() }
        verify(exactly = 0) { sheets.getValue("s1.jpg").recycle() }
        verify(exactly = 0) { sheets.getValue("s2.jpg").recycle() }

        // Back into the newest two: no download.
        provider.getThumbnail(11, loaded)
        provider.getThumbnail(21, loaded)
        scope.advanceUntilIdle()
        coVerify(exactly = 1) { repo.fetchSprite("item", "s1.jpg") }
        coVerify(exactly = 1) { repo.fetchSprite("item", "s2.jpg") }

        // Teardown recycles what is left.
        provider.release()
        verify(exactly = 1) { sheets.getValue("s1.jpg").recycle() }
        verify(exactly = 1) { sheets.getValue("s2.jpg").recycle() }
    }
}
