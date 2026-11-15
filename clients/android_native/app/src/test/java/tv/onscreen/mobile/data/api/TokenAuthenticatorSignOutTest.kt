package tv.onscreen.mobile.data.api

import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.coVerifyOrder
import io.mockk.mockk
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.ResponseBody.Companion.toResponseBody
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.mobile.data.downloads.DownloadOwner
import tv.onscreen.mobile.data.downloads.OnScreenDownloadManager
import tv.onscreen.mobile.data.prefs.ServerPrefs
import java.io.IOException

/**
 * The involuntary sign-out: a refresh the server definitively rejects clears
 * the tokens and locks the downloads to the identity being signed out (read
 * before the wipe), instead of erasing them. A transient refresh failure
 * touches neither.
 */
class TokenAuthenticatorSignOutTest {

    private lateinit var server: MockWebServer

    @Before
    fun setUp() {
        server = MockWebServer().apply { start() }
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private val downloads = mockk<OnScreenDownloadManager>(relaxed = true)

    private fun run(refreshFailure: Exception): ServerPrefs {
        val origin = server.url("/").toString().trimEnd('/')
        val prefs = mockk<ServerPrefs>(relaxed = true).also {
            coEvery { it.getServerUrl() } returns origin
            coEvery { it.getAccessToken() } returns "at"
            coEvery { it.getRefreshToken() } returns "rt"
            coEvery { it.getUserId() } returns "u1"
        }
        val api = mockk<OnScreenApi>().also { coEvery { it.refresh(any()) } throws refreshFailure }
        val interceptor = AuthInterceptor(prefs)
        val client = OkHttpClient.Builder()
            .addInterceptor(interceptor)
            .authenticator(TokenAuthenticator(prefs, { api }, interceptor) { downloads })
            .build()
        server.enqueue(MockResponse().setResponseCode(401))
        client.newCall(Request.Builder().url(server.url("/api/v1/hub")).build()).execute().close()
        return prefs
    }

    private fun rejected(code: Int) = HttpException(Response.error<Any>(code, "".toResponseBody(null)))

    @Test
    fun `a rejected refresh locks the downloads to the signed-out identity`() {
        val prefs = run(rejected(401))
        val owner = DownloadOwner.of(server.url("/").toString(), "u1")

        coVerifyOrder {
            prefs.getUserId()
            prefs.clearAuth()
            downloads.lockForSignOut(owner)
        }
        coVerify(exactly = 0) { downloads.cancelAllAndClear() }
    }

    @Test
    fun `a transient refresh failure leaves auth and downloads alone`() {
        val prefs = run(IOException("handoff"))

        coVerify(exactly = 0) { prefs.clearAuth() }
        coVerify(exactly = 0) { downloads.lockForSignOut(any()) }
    }
}
