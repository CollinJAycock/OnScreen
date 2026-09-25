package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.mobile.data.api.ApiResponse
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.TokenPair
import tv.onscreen.mobile.data.prefs.ServerPrefs

/** Sign-out runs after local auth is wiped, while the user may already be
 *  entering a different server. Its refresh + revoke must go to the server the
 *  session came from, never to whatever URL is configured by then. */
@OptIn(ExperimentalCoroutinesApi::class)
class AuthRepositoryLogoutTest {

    private fun rotated() = TokenPair(
        access_token = "at2", refresh_token = "rt2",
        expires_at = "2026-01-01T00:00:00Z",
        user_id = "u1", username = "alice", is_admin = false,
    )

    @Test
    fun `logout pins refresh and revoke to the original origin even if the server changes mid-flight`() = runTest {
        val api = mockk<OnScreenApi>(relaxed = true)
        val prefs = mockk<ServerPrefs>(relaxed = true)
        // First read (captured at sign-out start) is the old server; every
        // later read sees the new one the user typed while this ran.
        coEvery { prefs.getServerUrl() } returnsMany listOf("https://old.example:8443", "https://new.example")
        coEvery { prefs.getRefreshToken() } returns "rt-old"
        coEvery { prefs.getAccessToken() } returns "at-old"
        coEvery { api.refreshAt(any(), any()) } returns ApiResponse(rotated())

        val repo = AuthRepository(api, prefs, mockk(relaxed = true), mockk(relaxed = true))
        repo.logout()

        coVerify { api.refreshAt("https://old.example:8443/api/v1/auth/refresh", any()) }
        coVerify {
            api.logoutAt("https://old.example:8443/api/v1/auth/logout", any(), "Bearer at2")
        }
        // Never through the placeholder-base route, which re-reads the URL.
        coVerify(exactly = 0) { api.refresh(any()) }
        coVerify(exactly = 0) { api.logout(any(), any()) }
    }

    @Test
    fun `detached logout hands the captured origin to the follow-up`() = kotlinx.coroutines.runBlocking {
        // runBlocking, not runTest: the detached scope runs on real IO threads,
        // and runTest's virtual clock would fire the timeout immediately.
        val api = mockk<OnScreenApi>(relaxed = true)
        val prefs = mockk<ServerPrefs>(relaxed = true)
        coEvery { prefs.getServerUrl() } returnsMany listOf("http://10.0.0.5:7070/", "http://other:7070")
        coEvery { prefs.getRefreshToken() } returns null

        val repo = AuthRepository(api, prefs, mockk(relaxed = true), mockk(relaxed = true))
        val got = kotlinx.coroutines.CompletableDeferred<String?>()
        repo.logoutDetached(andThen = { origin -> got.complete(origin) })

        assertThat(kotlinx.coroutines.withTimeout(5_000) { got.await() }).isEqualTo("http://10.0.0.5:7070")
    }
}
