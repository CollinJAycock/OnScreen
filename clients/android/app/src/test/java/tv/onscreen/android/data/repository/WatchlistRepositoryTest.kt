package tv.onscreen.android.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.android.data.api.ApiResponse
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.WatchStatus
import tv.onscreen.android.data.model.WatchStatusUpdate

/** The per-item status calls the details page's Status action and the
 *  Watchlist card menu make. */
class WatchlistRepositoryTest {

    private val api = mockk<OnScreenApi>()
    private val repo = WatchlistRepository(api)

    private fun http(code: Int) = HttpException(Response.error<Any>(code, "".toResponseBody(null)))
    private fun failed(code: Int): Response<Unit> = Response.error(code, "".toResponseBody(null))

    @Test
    fun `status is the server's value`() = runTest {
        coEvery { api.getWatchStatus("m1") } returns ApiResponse(WatchStatus("watching", "2026-10-01T00:00:00Z"))

        assertThat(repo.status("m1")).isEqualTo("watching")
    }

    @Test
    fun `no status set (empty string, or 404 from an older server) is null`() = runTest {
        coEvery { api.getWatchStatus("none") } returns ApiResponse(WatchStatus(""))
        coEvery { api.getWatchStatus("old") } throws http(404)

        assertThat(repo.status("none")).isNull()
        assertThat(repo.status("old")).isNull()
    }

    @Test
    fun `any other status failure throws`() = runTest {
        coEvery { api.getWatchStatus("m1") } throws http(500)

        val e = runCatching { repo.status("m1") }.exceptionOrNull()
        assertThat((e as? HttpException)?.code()).isEqualTo(500)
    }

    @Test
    fun `setStatus PUTs the status`() = runTest {
        coEvery { api.setWatchStatus(any(), any()) } returns Response.success(Unit)

        repo.setStatus("m1", "on_hold")

        coVerify(exactly = 1) { api.setWatchStatus("m1", WatchStatusUpdate("on_hold")) }
    }

    @Test
    fun `clearStatus DELETEs the status`() = runTest {
        coEvery { api.clearWatchStatus(any()) } returns Response.success(Unit)

        repo.clearStatus("m1")

        coVerify(exactly = 1) { api.clearWatchStatus("m1") }
    }

    @Test
    fun `a non-2xx write throws (Response-typed calls don't on their own)`() = runTest {
        coEvery { api.setWatchStatus(any(), any()) } returns failed(429)
        coEvery { api.clearWatchStatus(any()) } returns failed(500)

        val put = runCatching { repo.setStatus("m1", "watching") }.exceptionOrNull()
        val del = runCatching { repo.clearStatus("m1") }.exceptionOrNull()
        assertThat((put as? HttpException)?.code()).isEqualTo(429)
        assertThat((del as? HttpException)?.code()).isEqualTo(500)
    }
}
