package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.moshi.MoshiConverterFactory
import tv.onscreen.mobile.data.api.OnScreenApi

/**
 * The pairing poll through real Retrofit + Moshi, against the server's actual
 * replies. The 202 "still pending" body has no `data`; parsed as an
 * ApiResponse it threw, every poll failed, and 0.5.0's poll loop showed
 * "Couldn't reach server" for the whole wait and gave up after eight tries.
 * The view-model tests fake pollPairing's result, so only a test that parses
 * the body catches it.
 */
class AuthRepositoryPairPollTest {

    private lateinit var server: MockWebServer
    private lateinit var repo: AuthRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(MoshiConverterFactory.create(Moshi.Builder().build()))
            .build()
            .create(OnScreenApi::class.java)
        repo = AuthRepository(api, mockk(relaxed = true), mockk(relaxed = true), mockk(relaxed = true), mockk(relaxed = true), mockk(relaxed = true), mockk(relaxed = true))
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    @Test
    fun `a pending 202 with no data is Pending, not a failure`() = runTest {
        server.enqueue(
            MockResponse().setResponseCode(202)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"status":"pending","expires_at":"2026-11-19T20:00:00Z"}"""),
        )

        assertThat(repo.pollPairing("device-token")).isEqualTo(AuthRepository.PollResult.Pending)
        assertThat(server.takeRequest().getHeader("Authorization")).isEqualTo("Bearer device-token")
    }

    @Test
    fun `a claimed 200 carries the token pair`() = runTest {
        server.enqueue(
            MockResponse().setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody(
                    """{"data":{"access_token":"a","refresh_token":"r","asset_token":"s",""" +
                        """"expires_at":"2026-11-19T21:00:00Z","user_id":"u1","username":"akadmin"}}""",
                ),
        )

        val result = repo.pollPairing("device-token")

        assertThat(result).isInstanceOf(AuthRepository.PollResult.Done::class.java)
        val pair = (result as AuthRepository.PollResult.Done).pair
        assertThat(pair.access_token).isEqualTo("a")
        assertThat(pair.username).isEqualTo("akadmin")
    }

    @Test
    fun `an expired code is Expired`() = runTest {
        server.enqueue(MockResponse().setResponseCode(410).setBody("""{"error":{"code":"EXPIRED"}}"""))

        assertThat(repo.pollPairing("device-token")).isEqualTo(AuthRepository.PollResult.Expired)
    }
}
