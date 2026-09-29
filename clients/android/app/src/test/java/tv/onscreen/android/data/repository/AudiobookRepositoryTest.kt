package tv.onscreen.android.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.android.data.api.ApiResponse
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.PlaybackRate
import tv.onscreen.android.data.model.PlaybackRateRequest

@OptIn(ExperimentalCoroutinesApi::class)
class AudiobookRepositoryTest {

    private fun http(code: Int) = HttpException(Response.error<Any>(code, "".toResponseBody(null)))

    private fun repo(api: OnScreenApi, scope: TestScope) =
        AudiobookRepository(api).also { it.detachedScope = scope }

    @Test
    fun `rate is the server's, clamped`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getPlaybackRate("ch-1") } returns ApiResponse(PlaybackRate(1.2500000476837158, "book"))

        assertThat(repo(api, this).rate("ch-1", "book-1")).isEqualTo(1.25f)
    }

    @Test
    fun `an older server or a failed lookup knows no rate`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getPlaybackRate("b404") } throws http(404)
        coEvery { api.getPlaybackRate("b500") } throws http(500)

        val r = repo(api, this)
        assertThat(r.rate("b404", "b404")).isNull()
        assertThat(r.rate("b500", "b500")).isNull()
    }

    @Test
    fun `a speed whose save hasn't landed wins over the server`() = runTest(StandardTestDispatcher()) {
        val api = mockk<OnScreenApi>()
        coEvery { api.getPlaybackRate(any()) } returns ApiResponse(PlaybackRate(1.0, "default"))
        coEvery { api.setPlaybackRate(any(), any()) } throws http(503)

        val r = repo(api, this)
        r.saveRate("ch-2", "book-1", 1.75f)
        advanceUntilIdle()

        // The PUT failed: still in effect on this device, for every chapter.
        assertThat(r.rate("ch-9", "book-1")).isEqualTo(1.75f)
        // Other books are untouched.
        assertThat(r.rate("book-2", "book-2")).isEqualTo(1.0f)
        coVerify { api.setPlaybackRate("ch-2", PlaybackRateRequest(1.75)) }
    }

    @Test
    fun `once saved, the server is the source again`() = runTest(StandardTestDispatcher()) {
        val api = mockk<OnScreenApi>()
        coEvery { api.setPlaybackRate(any(), any()) } returns Unit
        coEvery { api.getPlaybackRate(any()) } returns ApiResponse(PlaybackRate(2.5, "book"))

        val r = repo(api, this)
        r.saveRate("book-1", "book-1", 1.5f)
        advanceUntilIdle()

        // e.g. changed on the web since: the server's answer is used.
        assertThat(r.rate("book-1", "book-1")).isEqualTo(2.5f)
    }
}
