package tv.onscreen.mobile.data.api

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.mockk
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.downloads.DownloadStore
import tv.onscreen.mobile.data.prefs.ServerPrefs

/**
 * Regression guard for the credential-scoping boundary, ported from the TV
 * client's TokenAuthenticatorTest.
 *
 * The shared OkHttpClient is also Coil's image backend and fetches
 * server-supplied absolute third-party URLs (`DiscoverItem.poster_url`, copied
 * verbatim from TMDB with no host allow-listing server-side). Two things have
 * to hold:
 *
 *  1. [AuthInterceptor] must withhold the Bearer from any origin that is not
 *     the configured server — host AND port, so a neighbouring service on the
 *     same LAN box (Jellyfin :8096, Plex :32400) never receives it, and an
 *     injected `http://<same-host>/x.png` cannot downgrade an https-configured
 *     server to collect the token in the clear.
 *  2. [TokenAuthenticator] must repeat that check. OkHttp invokes the client
 *     Authenticator on a 401 from ANY host and dispatches its follow-up from
 *     inside RetryAndFollowUpInterceptor — BELOW the application interceptors
 *     — so the interceptor gets no second chance to strip what it adds.
 *
 * These run a real client against MockWebServer rather than asserting on
 * mocks, because the defect lives in the COMPOSITION of interceptor +
 * authenticator + retry loop. A mock-based test passed while the app leaked.
 */
class AuthScopingTest {

    private lateinit var server: MockWebServer
    private lateinit var thirdParty: MockWebServer

    @Before
    fun setUp() {
        server = MockWebServer().apply { start() }
        thirdParty = MockWebServer().apply { start() }
    }

    @After
    fun tearDown() {
        server.shutdown()
        thirdParty.shutdown()
    }

    private fun prefs(serverUrl: String): ServerPrefs = mockk<ServerPrefs>(relaxed = true).also {
        coEvery { it.getServerUrl() } returns serverUrl
        coEvery { it.getAccessToken() } returns TOKEN
        coEvery { it.getRefreshToken() } returns "refresh-tok"
    }

    private fun client(prefs: ServerPrefs): OkHttpClient {
        val interceptor = AuthInterceptor(prefs)
        val api = mockk<OnScreenApi>().also {
            coEvery { it.refresh(any()) } throws RuntimeException("refresh rejected")
        }
        val store = mockk<DownloadStore>(relaxed = true)
        return OkHttpClient.Builder()
            .addInterceptor(interceptor)
            .authenticator(TokenAuthenticator(prefs, { api }, interceptor) { store })
            .build()
    }

    private fun get(c: OkHttpClient, url: String) {
        c.newCall(Request.Builder().url(url).build()).execute().close()
    }

    // ── interceptor: first-request scoping ──────────────────────────────────

    @Test
    fun `bearer is attached to the configured server`() {
        server.enqueue(MockResponse().setResponseCode(200))
        val c = client(prefs(server.url("/").toString().trimEnd('/')))
        get(c, server.url("/api/v1/items").toString())
        assertThat(server.takeRequest().getHeader("Authorization")).isEqualTo("Bearer $TOKEN")
    }

    @Test
    fun `bearer is withheld from a different host`() {
        thirdParty.enqueue(MockResponse().setResponseCode(200))
        val c = client(prefs(server.url("/").toString().trimEnd('/')))
        get(c, thirdParty.url("/poster.jpg").toString())
        assertThat(thirdParty.takeRequest().getHeader("Authorization")).isNull()
    }

    @Test
    fun `bearer is withheld from the same host on a different port`() {
        // The Jellyfin/Plex-on-the-same-box case. Both MockWebServers bind
        // 127.0.0.1, so they share a host and differ only in port — exactly
        // the shape a host-only check would have let through.
        thirdParty.enqueue(MockResponse().setResponseCode(200))
        val c = client(prefs(server.url("/").toString().trimEnd('/')))
        get(c, thirdParty.url("/art.jpg").toString())
        val seen = thirdParty.takeRequest()
        assertThat(seen.requestUrl!!.host).isEqualTo(server.url("/").host)
        assertThat(seen.getHeader("Authorization")).isNull()
    }

    // ── authenticator: the 401 follow-up ────────────────────────────────────

    @Test
    fun `a third-party 401 does not receive the token on the retry`() {
        // Answer 401 twice: the first is the unauthenticated fetch, the second
        // would be the authenticator's follow-up if the guard were missing.
        thirdParty.enqueue(MockResponse().setResponseCode(401))
        thirdParty.enqueue(MockResponse().setResponseCode(401))
        val c = client(prefs(server.url("/").toString().trimEnd('/')))
        get(c, thirdParty.url("/poster.jpg").toString())

        val first = thirdParty.takeRequest()
        assertThat(first.getHeader("Authorization")).isNull()
        // The guard should stop the retry entirely. If one is made anyway, it
        // must not carry the token.
        val retry = thirdParty.takeRequest(1, java.util.concurrent.TimeUnit.SECONDS)
        if (retry != null) {
            assertThat(retry.getHeader("Authorization")).isNull()
        }
        assertThat(thirdParty.requestCount).isEqualTo(1)
    }

    @Test
    fun `a same-host different-port 401 does not receive the token`() {
        thirdParty.enqueue(MockResponse().setResponseCode(401))
        thirdParty.enqueue(MockResponse().setResponseCode(401))
        val c = client(prefs(server.url("/").toString().trimEnd('/')))
        get(c, thirdParty.url("/art.jpg").toString())
        assertThat(thirdParty.requestCount).isEqualTo(1)
        assertThat(thirdParty.takeRequest().getHeader("Authorization")).isNull()
    }

    @Test
    fun `the configured server still gets a refresh attempt on 401`() {
        // The guard must not break the real refresh path: a 401 from OUR
        // origin should still reach the authenticator (refresh throws here, so
        // it gives up after the attempt rather than looping).
        server.enqueue(MockResponse().setResponseCode(401))
        val c = client(prefs(server.url("/").toString().trimEnd('/')))
        get(c, server.url("/api/v1/items").toString())
        assertThat(server.takeRequest().getHeader("Authorization")).isEqualTo("Bearer $TOKEN")
    }

    private companion object {
        const val TOKEN = "access-tok"
    }
}
