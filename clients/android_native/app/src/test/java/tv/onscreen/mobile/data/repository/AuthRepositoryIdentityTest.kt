package tv.onscreen.mobile.data.repository

import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.coVerifyOrder
import io.mockk.mockk
import io.mockk.verify
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.mobile.data.IdentityCaches
import tv.onscreen.mobile.data.api.ApiResponse
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.downloads.DownloadOwner
import tv.onscreen.mobile.data.downloads.OnScreenDownloadManager
import tv.onscreen.mobile.data.model.TokenPair
import tv.onscreen.mobile.data.prefs.ServerPrefs

/** Every identity change empties the device-side caches (IdentityCaches), and
 *  a sign-in claims the downloads for its server + user before the tokens
 *  land: locked ones of the same identity come back, anyone else's go. */
class AuthRepositoryIdentityTest {

    private val api = mockk<OnScreenApi>(relaxed = true)
    private val prefs = mockk<ServerPrefs>(relaxed = true).also {
        coEvery { it.getServerUrl() } returns "https://media.example.com"
    }
    private val caches = mockk<IdentityCaches>(relaxed = true)
    private val downloads = mockk<OnScreenDownloadManager>(relaxed = true)
    private val repo = AuthRepository(
        api, prefs, mockk(relaxed = true), mockk(relaxed = true), mockk(relaxed = true), caches, downloads,
    )

    private fun pair(totp: Boolean = false) = TokenPair(
        access_token = if (totp) "" else "at", refresh_token = if (totp) "" else "rt",
        expires_at = "2026-01-01T00:00:00Z", user_id = "u1", username = "alice", is_admin = false,
        totp_required = totp,
    )

    @Test
    fun `a sign-in claims the downloads before storing the tokens`() = runTest {
        coEvery { api.login(any()) } returns ApiResponse(pair())

        repo.login("alice", "pw")

        coVerifyOrder {
            caches.clear()
            downloads.claimFor(DownloadOwner("https://media.example.com", "u1"))
            prefs.setTokens("at", "rt", any())
        }
    }

    @Test
    fun `pairing claims them too`() = runTest {
        repo.completePairing(pair())

        coVerify { downloads.claimFor(DownloadOwner("https://media.example.com", "u1")) }
        verify { caches.clear() }
    }

    @Test
    fun `a password step that still owes a second factor claims nothing`() = runTest {
        coEvery { api.login(any()) } returns ApiResponse(pair(totp = true))

        repo.login("alice", "pw")

        coVerify(exactly = 0) { downloads.claimFor(any()) }
    }

    @Test
    fun `sign-outs empty the caches`() = runTest {
        repo.logout()
        repo.onInvoluntarySignOut()

        verify(exactly = 2) { caches.clear() }
    }
}
