package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.Response
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.prefs.ServerPrefs
import java.io.IOException

/** checkServer writes the URL before probing it (the request is routed by
 *  it), and must put the previous one back unless the probe succeeds — as
 *  the TV client does. A typo'd address used to stay stored. */
@OptIn(ExperimentalCoroutinesApi::class)
class AuthRepositoryCheckServerTest {

    private val api = mockk<OnScreenApi>(relaxed = true)
    private val prefs = mockk<ServerPrefs>(relaxed = true).also {
        coEvery { it.getServerUrl() } returns "https://old.example"
    }
    private val repo = AuthRepository(api, prefs, mockk(relaxed = true), mockk(relaxed = true), mockk(relaxed = true), mockk(relaxed = true), mockk(relaxed = true))

    @Test
    fun `an unreachable server puts the previous url back`() = runTest {
        coEvery { api.healthCheck() } throws IOException("no route")

        val result = repo.checkServer("https://typo.example")

        assertThat(result).isInstanceOf(AuthRepository.CheckResult.Failed::class.java)
        coVerify { prefs.setServerUrl("https://typo.example") }
        coVerify { prefs.restoreServerUrl("https://typo.example", "https://old.example") }
    }

    @Test
    fun `an http error puts the previous url back`() = runTest {
        coEvery { api.healthCheck() } returns Response.error(404, "".toResponseBody(null))

        assertThat(repo.checkServer("https://wrong.example"))
            .isEqualTo(AuthRepository.CheckResult.HttpError(404))
        coVerify { prefs.restoreServerUrl("https://wrong.example", "https://old.example") }
    }

    @Test
    fun `a first-run failure leaves no server`() = runTest {
        coEvery { prefs.getServerUrl() } returns null
        coEvery { api.healthCheck() } throws IOException("no route")

        repo.checkServer("https://typo.example")

        coVerify { prefs.restoreServerUrl("https://typo.example", null) }
    }

    @Test
    fun `a reachable server is kept`() = runTest {
        coEvery { api.healthCheck() } returns Response.success(Unit)

        assertThat(repo.checkServer("http://10.0.0.5:7070")).isEqualTo(AuthRepository.CheckResult.Ok)
        coVerify(exactly = 0) { prefs.restoreServerUrl(any(), any()) }
    }

    @Test
    fun `an abandoned probe puts the previous url back`() = runTest {
        val never = CompletableDeferred<Response<Unit>>()
        coEvery { api.healthCheck() } coAnswers { never.await() }

        val job = launch { repo.checkServer("https://slow.example") }
        advanceUntilIdle()
        job.cancel()
        advanceUntilIdle()

        coVerify { prefs.restoreServerUrl("https://slow.example", "https://old.example") }
    }
}
