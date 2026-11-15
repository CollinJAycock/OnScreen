package tv.onscreen.android.data.repository

import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.coVerifyOrder
import io.mockk.mockk
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Test
import tv.onscreen.android.data.api.ApiResponse
import tv.onscreen.android.data.api.AuthInterceptor
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.LogoutRequest
import tv.onscreen.android.data.model.TokenPair
import tv.onscreen.android.data.prefs.ServerPrefs

/** Sign-out clears the TV first and revokes afterwards, detached, against
 *  the server the session belonged to; sign-in stores its tokens before
 *  anything re-dials with them. */
class AuthRepositoryTest {

    private val authInterceptor = mockk<AuthInterceptor>(relaxed = true)

    private fun prefs(serverUrls: List<String?> = listOf("https://old.example")): ServerPrefs =
        mockk<ServerPrefs>(relaxed = true).also {
            coEvery { it.getServerUrl() } returnsMany serverUrls
            coEvery { it.getRefreshToken() } returns "refresh-1"
            coEvery { it.getAccessToken() } returns "access-1"
        }

    private fun repo(prefs: ServerPrefs, authApi: OnScreenApi) = AuthRepository(
        api = mockk(), // the main client: sign-out must not use it
        authApi = authApi,
        prefs = prefs,
        preferences = mockk(relaxed = true),
        capabilities = mockk(relaxed = true),
        notifications = mockk(relaxed = true),
        authInterceptor = authInterceptor,
    )

    @Test
    fun `local auth is cleared at once even while the server never answers`() = runBlocking {
        val prefs = prefs()
        val authApi = mockk<OnScreenApi>().also {
            coEvery { it.refreshAt(any(), any()) } coAnswers { awaitCancellation() }
        }

        // Returns without waiting on the revoke (it used to wait out both
        // network calls before clearing anything).
        withTimeout(2_000) { repo(prefs, authApi).logout() }

        coVerify(exactly = 1) { prefs.clearAuth() }
        coVerify { authInterceptor.invalidateCache() }
    }

    @Test
    fun `a caller cancelled mid sign-out still leaves the TV signed out`() = runBlocking {
        // BACK, BACK finishing the activity cancels the scope sign-out runs on.
        val prefs = prefs()
        coEvery { prefs.getRefreshToken() } coAnswers { delay(200); "refresh-1" }
        val authApi = mockk<OnScreenApi>(relaxed = true)

        val job = launch { repo(prefs, authApi).logout() }
        delay(50)
        job.cancel()
        job.join()

        coVerify(exactly = 1) { prefs.clearAuth() }
    }

    @Test
    fun `the revoke rotates then revokes on the captured origin with the rotated pair`() = runBlocking {
        // The user is already entering a new server while the revoke runs.
        val prefs = prefs(listOf("https://old.example", "https://new.example"))
        val authApi = mockk<OnScreenApi>(relaxed = true).also {
            coEvery { it.refreshAt(any(), any()) } returns ApiResponse(TokenPair("access-2", "refresh-2"))
        }

        repo(prefs, authApi).logout()

        coVerify(timeout = 5_000) {
            authApi.logoutAt("https://old.example/api/v1/auth/logout", LogoutRequest("refresh-2"), "Bearer access-2")
        }
        coVerifyOrder {
            prefs.clearAuth()
            authApi.refreshAt("https://old.example/api/v1/auth/refresh", any())
            authApi.logoutAt(any(), any(), any())
        }
    }

    @Test
    fun `a failed rotation still revokes the stored pair`() = runBlocking {
        val prefs = prefs()
        val authApi = mockk<OnScreenApi>(relaxed = true).also {
            coEvery { it.refreshAt(any(), any()) } throws java.io.IOException("unreachable")
        }

        repo(prefs, authApi).logout()

        coVerify(timeout = 5_000) {
            authApi.logoutAt("https://old.example/api/v1/auth/logout", LogoutRequest("refresh-1"), "Bearer access-1")
        }
    }

    // ── Sign-in order ──────────────────────────────────────────────────────

    @Test
    fun `sign-in stores the tokens before it re-dials the notifications stream`() = runBlocking {
        // The re-dial used to go first: it went out bare, and its 401 had
        // TokenAuthenticator clear auth right after the sign-in's write.
        val prefs = prefs()
        io.mockk.every { prefs.serverUrl } returns kotlinx.coroutines.flow.flowOf("https://old.example")
        val api = mockk<OnScreenApi>().also {
            coEvery { it.login(any()) } returns ApiResponse(TokenPair("a", "r", user_id = "u1", username = "alice"))
        }
        val notifications = mockk<NotificationsRepository>(relaxed = true)
        val repo = AuthRepository(
            api, mockk(), prefs, mockk(relaxed = true), mockk(relaxed = true), notifications, authInterceptor,
        )

        repo.login("alice", "pw")

        coVerifyOrder {
            prefs.setTokens("a", "r", any())
            prefs.setUser("u1", "alice")
            authInterceptor.invalidateCache()
            notifications.restartForIdentityChange()
        }
    }

    @Test
    fun `pairing stores the tokens before it re-dials the notifications stream`() = runBlocking {
        val prefs = prefs()
        val notifications = mockk<NotificationsRepository>(relaxed = true)
        val repo = AuthRepository(
            mockk(), mockk(), prefs, mockk(relaxed = true), mockk(relaxed = true), notifications, authInterceptor,
        )

        repo.completePairing(TokenPair("a", "r", user_id = "u1", username = "alice"))

        coVerifyOrder {
            prefs.setTokens("a", "r", any())
            authInterceptor.invalidateCache()
            notifications.restartForIdentityChange()
        }
    }

    @Test
    fun `a localhost origin is not revoked once another server is configured`() = runBlocking {
        // BaseUrlInterceptor re-points `localhost` at the CURRENT server.
        val prefs = prefs(listOf("http://localhost:7070", "https://new.example"))
        val authApi = mockk<OnScreenApi>(relaxed = true)

        repo(prefs, authApi).logout()
        delay(300)

        coVerify(exactly = 0) { authApi.refreshAt(any(), any()) }
        coVerify(exactly = 0) { authApi.logoutAt(any(), any(), any()) }
        coVerify(exactly = 1) { prefs.clearAuth() }
    }
}
