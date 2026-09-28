package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.mobile.data.api.ApiListResponse
import tv.onscreen.mobile.data.api.ApiResponse
import tv.onscreen.mobile.data.api.Meta
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.RandomLibraryItem
import tv.onscreen.mobile.data.model.UpNext

/** The v2.5 watch-state calls through the repositories: which endpoint each
 *  one hits and how the documented status codes map. */
class WatchStateRepositoryTest {

    private fun http(code: Int) = HttpException(Response.error<Unit>(code, "".toResponseBody(null)))

    @Test
    fun `setWatched routes to POST or DELETE watched`() = runTest {
        val api = mockk<OnScreenApi>(relaxed = true)
        val repo = ItemRepository(api)
        repo.setWatched("a", true)
        repo.setWatched("b", false)
        coVerify(exactly = 1) { api.markWatched("a") }
        coVerify(exactly = 1) { api.markUnwatched("b") }
    }

    @Test
    fun `setWatched surfaces the rate limit`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.markWatched("a") } throws http(429)
        val e = runCatching { ItemRepository(api).setWatched("a", true) }.exceptionOrNull()
        assertThat((e as HttpException).code()).isEqualTo(429)
    }

    @Test
    fun `getUpNext unwraps the envelope`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getUpNext("show") } returns ApiResponse(UpNext("rewatch"))
        assertThat(ItemRepository(api).getUpNext("show").mode).isEqualTo("rewatch")
    }

    @Test
    fun `dismiss treats 404 (nothing left to hide) as success`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.dismissContinueWatching("gone") } throws http(404)
        coEvery { api.dismissContinueWatching("err") } throws http(500)
        val repo = HubRepository(api)
        repo.dismissContinueWatching("gone") // no throw
        val e = runCatching { repo.dismissContinueWatching("err") }.exceptionOrNull()
        assertThat((e as HttpException).code()).isEqualTo(500)
    }

    @Test
    fun `random pick maps 404 to null and passes the filters`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getRandomLibraryItem("L", null, "unwatched") } returns ApiResponse(RandomLibraryItem("x", "movie"))
        coEvery { api.getRandomLibraryItem("L", null, "watched") } throws http(404)
        coEvery { api.getRandomLibraryItem("L", null, null) } throws http(501)
        val repo = LibraryRepository(api)
        assertThat(repo.randomItem("L", watch = "unwatched")).isEqualTo(RandomLibraryItem("x", "movie"))
        assertThat(repo.randomItem("L", watch = "watched")).isNull()
        val e = runCatching { repo.randomItem("L") }.exceptionOrNull()
        assertThat((e as HttpException).code()).isEqualTo(501)
    }

    @Test
    fun `library listing forwards the watch filter`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getLibraryItems("L", 100, 0, null, null, null, "in_progress") } returns
            ApiListResponse(emptyList(), Meta(total = 0, cursor = null))
        LibraryRepository(api).getItems("L", limit = 100, offset = 0, watch = "in_progress")
        coVerify(exactly = 1) { api.getLibraryItems("L", 100, 0, null, null, null, "in_progress") }
    }
}
