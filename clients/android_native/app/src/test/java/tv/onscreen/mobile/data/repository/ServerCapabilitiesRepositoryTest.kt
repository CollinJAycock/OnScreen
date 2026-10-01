package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import com.squareup.moshi.Types
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.mobile.data.api.ApiResponse
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.ServerCapabilities
import tv.onscreen.mobile.data.model.ServerFeatures
import tv.onscreen.mobile.data.prefs.ServerPrefs

/**
 * The server capabilities the player asks before it sends a progress report
 * without a duration: once per server, "no" whenever it can't tell.
 */
class ServerCapabilitiesRepositoryTest {

    private fun caps(progressWithoutDuration: Boolean) =
        ApiResponse(ServerCapabilities(ServerFeatures(progress_without_duration = progressWithoutDuration)))

    private fun prefs(vararg urls: String?): ServerPrefs = mockk<ServerPrefs>().also { p ->
        coEvery { p.getServerUrl() } returnsMany urls.toList()
    }

    @Test
    fun `a server that says so takes reports without a duration, asked once`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getCapabilities() } returns caps(true)
        val repo = ServerCapabilitiesRepository(api, prefs("http://srv", "http://srv/"))

        assertThat(repo.progressWithoutDuration()).isTrue()
        assertThat(repo.progressWithoutDuration()).isTrue()
        coVerify(exactly = 1) { api.getCapabilities() }
    }

    @Test
    fun `a server that doesn't say so doesn't`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getCapabilities() } returns caps(false)
        val repo = ServerCapabilitiesRepository(api, prefs("http://srv"))

        assertThat(repo.progressWithoutDuration()).isFalse()
    }

    @Test
    fun `a failed fetch reads as no and is asked again next time`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getCapabilities() } throws java.io.IOException("unreachable") andThen caps(true)
        val repo = ServerCapabilitiesRepository(api, prefs("http://srv", "http://srv"))

        assertThat(repo.progressWithoutDuration()).isFalse()
        assertThat(repo.progressWithoutDuration()).isTrue()
        coVerify(exactly = 2) { api.getCapabilities() }
    }

    @Test
    fun `another server is asked for itself`() = runTest {
        // Settings -> change server: the old server's answer is not the new one's.
        val api = mockk<OnScreenApi>()
        coEvery { api.getCapabilities() } returnsMany listOf(caps(true), caps(false))
        val repo = ServerCapabilitiesRepository(api, prefs("http://old", "http://new"))

        assertThat(repo.progressWithoutDuration()).isTrue()
        assertThat(repo.progressWithoutDuration()).isFalse()
        coVerify(exactly = 2) { api.getCapabilities() }
    }

    @Test
    fun `no server configured asks nothing`() = runTest {
        val api = mockk<OnScreenApi>()
        val repo = ServerCapabilitiesRepository(api, prefs(null))

        assertThat(repo.progressWithoutDuration()).isFalse()
        coVerify(exactly = 0) { api.getCapabilities() }
    }

    @Test
    fun `the flag decodes, and a server without it reads as false`() {
        val type = Types.newParameterizedType(ApiResponse::class.java, ServerCapabilities::class.java)
        val adapter = Moshi.Builder().build().adapter<ApiResponse<ServerCapabilities>>(type)

        val current = adapter.fromJson(
            """{"data":{"server":{"name":"s"},"features":{"transcode":true,"progress_without_duration":true}}}""",
        )
        assertThat(current!!.data.features.progress_without_duration).isTrue()

        val older = adapter.fromJson("""{"data":{"server":{"name":"s"},"features":{"transcode":true}}}""")
        assertThat(older!!.data.features.progress_without_duration).isFalse()
    }
}
