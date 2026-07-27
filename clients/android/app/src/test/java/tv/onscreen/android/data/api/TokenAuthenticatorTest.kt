package tv.onscreen.android.data.api

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.mockk
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.android.data.model.TokenPair
import tv.onscreen.android.data.prefs.ServerPrefs

/**
 * Regression guard for the token-leak defect: the shared OkHttpClient is also
 * Coil's image backend and fetches third-party URLs (TMDB Discover posters,
 * M3U channel logos). [AuthInterceptor] withholds the Bearer from non-server
 * hosts, but OkHttp invokes the client Authenticator on a 401 from ANY host,
 * and dispatches its follow-up request from inside RetryAndFollowUpInterceptor
 * — BELOW the application interceptors — so the interceptor never gets a
 * second chance to strip a header the authenticator adds. Without a matching
 * host check in [TokenAuthenticator], any third-party host that answers 401
 * receives the user's OnScreen access token.
 *
 * The end-to-end cases run a real OkHttpClient against MockWebServer rather
 * than asserting on mocks, because the defect lives in the *composition* of
 * interceptor + authenticator + OkHttp's retry loop. A mock-based test would
 * have passed while the app leaked.
 */
class TokenAuthenticatorTest {

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

    private fun prefs(
        serverUrl: String,
        accessToken: String? = "access-tok",
        refreshToken: String? = "refresh-tok",
    ): ServerPrefs = mockk<ServerPrefs>(relaxed = true).also {
        coEvery { it.getServerUrl() } returns serverUrl
        coEvery { it.getAccessToken() } returns accessToken
        coEvery { it.getRefreshToken() } returns refreshToken
    }

    private fun api(pair: TokenPair? = null): OnScreenApi = mockk<OnScreenApi>().also {
        if (pair != null) {
            coEvery { it.refresh(any()) } returns ApiResponse(pair)
        } else {
            coEvery { it.refresh(any()) } throws RuntimeException("refresh rejected")
        }
    }

    private fun client(prefs: ServerPrefs, api: OnScreenApi): OkHttpClient =
        OkHttpClient.Builder()
            .addInterceptor(AuthInterceptor(prefs))
            .authenticator(TokenAuthenticator(prefs) { api })
            .build()

    private fun unauthorizedFrom(url: String): Response = Response.Builder()
        .request(Request.Builder().url(url).build())
        .protocol(Protocol.HTTP_1_1)
        .code(401)
        .message("Unauthorized")
        .build()

    // ── The leak, end to end ────────────────────────────────────────────────

    @Test
    fun `third-party host answering 401 never receives the bearer token`() {
        // Both MockWebServers bind to localhost, so this pair differs only by
        // port — which is precisely the shared-LAN-host case (OnScreen and a
        // neighbouring service on one box) the origin check has to cover.
        val prefs = prefs(serverUrl = server.url("/").toString().trimEnd('/'))
        val http = client(prefs, api(TokenPair("fresh-access", "fresh-refresh")))

        // A hotlink-protected logo CDN / an on-path attacker on a cleartext
        // LAN URL answers 401. Queue two so a retry would be served, not
        // merely blocked by an empty dispatcher queue.
        repeat(2) { thirdParty.enqueue(MockResponse().setResponseCode(401)) }

        http.newCall(Request.Builder().url(thirdParty.url("/logo.png")).build())
            .execute().close()

        // Exactly one request: the authenticator declined to retry.
        assertThat(thirdParty.requestCount).isEqualTo(1)
        val sent = thirdParty.takeRequest()
        assertThat(sent.getHeader("Authorization")).isNull()
        // The capabilities header is likewise server-scoped and must not leak
        // a device fingerprint to third parties.
        assertThat(sent.getHeader("X-Client-Capabilities")).isNull()
    }

    @Test
    fun `third-party 401 does not spend a token refresh`() {
        val prefs = prefs(serverUrl = server.url("/").toString().trimEnd('/'))
        val refreshApi = mockk<OnScreenApi>() // unstubbed: any refresh() call throws
        val http = OkHttpClient.Builder()
            .addInterceptor(AuthInterceptor(prefs))
            .authenticator(TokenAuthenticator(prefs) { refreshApi })
            .build()

        thirdParty.enqueue(MockResponse().setResponseCode(401))

        // Must not throw — a rogue host's 401 must not drive a refresh (which
        // would also rotate the refresh token in response to an unrelated
        // host, invalidating the real session).
        val response = http.newCall(
            Request.Builder().url(thirdParty.url("/poster.jpg")).build(),
        ).execute()
        assertThat(response.code).isEqualTo(401)
        response.close()
    }

    // ── The happy path still works ──────────────────────────────────────────

    @Test
    fun `our server 401 refreshes and retries with the new token`() {
        val prefs = prefs(serverUrl = server.url("/").toString().trimEnd('/'))
        val http = client(prefs, api(TokenPair("fresh-access", "fresh-refresh")))

        server.enqueue(MockResponse().setResponseCode(401))
        server.enqueue(MockResponse().setResponseCode(200).setBody("{}"))

        val response = http.newCall(
            Request.Builder().url(server.url("/api/v1/hub")).build(),
        ).execute()

        assertThat(response.code).isEqualTo(200)
        response.close()
        assertThat(server.requestCount).isEqualTo(2)

        val first = server.takeRequest()
        assertThat(first.getHeader("Authorization")).isEqualTo("Bearer access-tok")
        val retry = server.takeRequest()
        assertThat(retry.getHeader("Authorization")).isEqualTo("Bearer fresh-access")
    }

    // ── Unit-level guards on authenticate() itself ──────────────────────────

    @Test
    fun `authenticate returns null for a non-server host`() {
        val prefs = prefs(serverUrl = "https://media.example.com")
        val auth = TokenAuthenticator(prefs) { api(TokenPair("a", "b")) }

        val result = auth.authenticate(null, unauthorizedFrom("https://evil.example/x.png"))

        assertThat(result).isNull()
    }

    @Test
    fun `authenticate proceeds for the configured server host`() {
        val prefs = prefs(serverUrl = "https://media.example.com", accessToken = "current")
        val auth = TokenAuthenticator(prefs) { api(TokenPair("rotated", "r")) }

        // usedToken (absent) differs from the cached token, so the fast path
        // retries with the cached one rather than spending a refresh.
        val result = auth.authenticate(null, unauthorizedFrom("https://media.example.com/api/v1/hub"))

        assertThat(result).isNotNull()
        assertThat(result!!.header("Authorization")).isEqualTo("Bearer current")
    }

    @Test
    fun `host match is case-insensitive`() {
        val prefs = prefs(serverUrl = "http://Media.Example.com:7070", accessToken = "current")
        val auth = TokenAuthenticator(prefs) { api(TokenPair("a", "b")) }

        val result = auth.authenticate(null, unauthorizedFrom("http://media.example.com:7070/api/v1/hub"))

        assertThat(result).isNotNull()
    }

    @Test
    fun `a different port on the same host is not our server`() {
        // On a home LAN the media box commonly runs several servers on one
        // host (`:7070` OnScreen, `:8096` Jellyfin, `:32400` Plex). A
        // host-only match would hand the OnScreen token to whichever
        // neighbour answered 401.
        val prefs = prefs(serverUrl = "http://10.0.0.5:7070", accessToken = "current")
        val auth = TokenAuthenticator(prefs) { api(TokenPair("a", "b")) }

        assertThat(auth.authenticate(null, unauthorizedFrom("http://10.0.0.5:8096/x"))).isNull()
        assertThat(auth.authenticate(null, unauthorizedFrom("http://10.0.0.5:32400/x"))).isNull()
        // The configured origin itself still works.
        assertThat(auth.authenticate(null, unauthorizedFrom("http://10.0.0.5:7070/api/v1/hub"))).isNotNull()
    }

    @Test
    fun `an http downgrade of an https server is not our server`() {
        // https default port 443 vs http default 80 — an on-path attacker
        // answering the cleartext origin must not collect the token.
        val prefs = prefs(serverUrl = "https://media.example.com", accessToken = "current")
        val auth = TokenAuthenticator(prefs) { api(TokenPair("a", "b")) }

        assertThat(auth.authenticate(null, unauthorizedFrom("http://media.example.com/api/v1/hub"))).isNull()
        assertThat(auth.authenticate(null, unauthorizedFrom("https://media.example.com/api/v1/hub"))).isNotNull()
    }

    @Test
    fun `a host that merely contains the server host is not treated as the server`() {
        val prefs = prefs(serverUrl = "https://media.example.com")
        val auth = TokenAuthenticator(prefs) { api(TokenPair("a", "b")) }

        // Substring/suffix confusion must not authorise these.
        val lookalikes = listOf(
            "https://media.example.com.evil.test/x",
            "https://evil.test/?u=media.example.com",
            "https://notmedia.example.com/x",
        )
        lookalikes.forEach { url ->
            assertThat(auth.authenticate(null, unauthorizedFrom(url))).isNull()
        }
    }

    @Test
    fun `refresh and login 401s are never retried`() {
        val prefs = prefs(serverUrl = "https://media.example.com")
        val auth = TokenAuthenticator(prefs) { api(TokenPair("a", "b")) }

        assertThat(
            auth.authenticate(null, unauthorizedFrom("https://media.example.com/api/v1/auth/refresh")),
        ).isNull()
        assertThat(
            auth.authenticate(null, unauthorizedFrom("https://media.example.com/api/v1/auth/login")),
        ).isNull()
    }

    @Test
    fun `unconfigured server does not authorise an arbitrary host`() {
        // Before first-run setup there is no server URL. A null host must not
        // fall open into "attach the token to whatever answered 401".
        val prefs = mockk<ServerPrefs>(relaxed = true).also {
            coEvery { it.getServerUrl() } returns null
            coEvery { it.getAccessToken() } returns null
            coEvery { it.getRefreshToken() } returns null
        }
        val auth = TokenAuthenticator(prefs) { api() }

        // No tokens exist, so there is nothing to leak and no refresh to spend.
        assertThat(auth.authenticate(null, unauthorizedFrom("https://evil.example/x"))).isNull()
    }
}
