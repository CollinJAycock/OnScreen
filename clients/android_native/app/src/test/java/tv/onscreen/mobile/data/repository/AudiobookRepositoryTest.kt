package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.mobile.data.api.ApiResponse
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.Bookmark
import tv.onscreen.mobile.data.model.BookmarkNoteRequest
import tv.onscreen.mobile.data.model.CreateBookmarkRequest
import tv.onscreen.mobile.data.model.PlaybackRate
import tv.onscreen.mobile.data.model.PlaybackRateRequest
import java.io.IOException

@OptIn(ExperimentalCoroutinesApi::class)
class AudiobookRepositoryTest {

    private fun http(code: Int) = HttpException(Response.error<Any>(code, "".toResponseBody(null)))

    private fun repo(api: OnScreenApi, scope: TestScope) =
        AudiobookRepository(api).also { it.detachedScope = scope }

    @Test
    fun `speed is the server's, clamped`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getPlaybackRate("ch-1") } returns ApiResponse(PlaybackRate(1.2500000476837158, "book"))

        assertThat(repo(api, this).listeningSpeed("ch-1", "book-1"))
            .isEqualTo(ListeningSpeed(1.25f, serverSupport = true))
    }

    @Test
    fun `a 404 is a server without the feature`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getPlaybackRate(any()) } throws http(404)

        assertThat(repo(api, this).listeningSpeed("b", "b"))
            .isEqualTo(ListeningSpeed(null, serverSupport = false))
    }

    @Test
    fun `any other failure is nothing known, still supported`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getPlaybackRate("b500") } throws http(500)
        coEvery { api.getPlaybackRate("off") } throws IOException("offline")

        val r = repo(api, this)
        assertThat(r.listeningSpeed("b500", "b500")).isEqualTo(ListeningSpeed(null, true))
        assertThat(r.listeningSpeed("off", "off")).isEqualTo(ListeningSpeed(null, true))
    }

    @Test
    fun `a speed whose save hasn't landed wins, on every chapter of the book`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getPlaybackRate(any()) } returns ApiResponse(PlaybackRate(1.0, "default"))
        coEvery { api.setPlaybackRate(any(), any()) } throws http(404)

        val r = repo(api, this)
        r.saveRate("ch-2", "book-1", 1.75f)
        advanceUntilIdle()

        // An older server refused the save: the speed still works here.
        assertThat(r.listeningSpeed("ch-9", "book-1").rate).isEqualTo(1.75f)
        assertThat(r.listeningSpeed("book-2", "book-2").rate).isEqualTo(1.0f)
        coVerify { api.setPlaybackRate("ch-2", PlaybackRateRequest(1.75)) }
    }

    @Test
    fun `offline, a speed picked this session is still known`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.setPlaybackRate(any(), any()) } throws IOException("offline")
        coEvery { api.getPlaybackRate(any()) } throws IOException("offline")

        val r = repo(api, this)
        r.saveRate("book-1", "book-1", 2.5f)
        advanceUntilIdle()

        assertThat(r.listeningSpeed("book-1", "book-1")).isEqualTo(ListeningSpeed(2.5f, true))
    }

    @Test
    fun `once saved, the server is the source again`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.setPlaybackRate(any(), any()) } returns Unit
        coEvery { api.getPlaybackRate(any()) } returns ApiResponse(PlaybackRate(2.0, "book"))

        val r = repo(api, this)
        r.saveRate("book-1", "book-1", 9f)
        advanceUntilIdle()

        coVerify { api.setPlaybackRate("book-1", PlaybackRateRequest(3.0)) }
        assertThat(r.listeningSpeed("book-1", "book-1").rate).isEqualTo(2.0f)
    }

    @Test
    fun `bookmarks from an older server are null, other failures throw`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.listBookmarks("old") } throws http(404)
        coEvery { api.listBookmarks("broken") } throws http(500)
        coEvery { api.listBookmarks("book") } returns ApiResponse(
            listOf(Bookmark(id = "1", item_id = "book", position_ms = 5_000)),
        )

        val r = repo(api, this)
        assertThat(r.bookmarks("old")).isNull()
        assertThat(r.bookmarks("book")?.map { it.id }).containsExactly("1")
        val thrown = runCatching { r.bookmarks("broken") }.exceptionOrNull()
        assertThat(thrown).isInstanceOf(HttpException::class.java)
    }

    @Test
    fun `notes go out trimmed`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.createBookmark(any(), any()) } returns ApiResponse(
            Bookmark(id = "1", item_id = "ch-1", position_ms = 0),
        )
        coEvery { api.updateBookmark(any(), any()) } returns Unit

        val r = repo(api, this)
        r.addBookmark("ch-1", -40, "  the storm  ")
        r.updateNote("1", "  ")

        coVerify { api.createBookmark("ch-1", CreateBookmarkRequest(0, "the storm")) }
        coVerify { api.updateBookmark("1", BookmarkNoteRequest("")) }
    }
}
