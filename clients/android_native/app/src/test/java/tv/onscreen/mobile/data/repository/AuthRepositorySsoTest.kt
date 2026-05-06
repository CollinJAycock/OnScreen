package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.mobile.data.api.ApiResponse
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.AuthProviderStatus
import tv.onscreen.mobile.data.model.LoginRequest
import tv.onscreen.mobile.data.model.TokenPair
import tv.onscreen.mobile.data.prefs.ServerPrefs

@OptIn(ExperimentalCoroutinesApi::class)
class AuthRepositorySsoTest {

    private fun status(enabled: Boolean, name: String = "Provider") =
        AuthProviderStatus(enabled = enabled, display_name = name)

    private fun pair() = TokenPair(
        access_token = "at", refresh_token = "rt",
        expires_at = "2026-01-01T00:00:00Z",
        user_id = "u1", username = "alice", is_admin = false,
    )

    @Test
    fun `getAuthProviders returns all three flags when every probe succeeds`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getLdapEnabled() } returns ApiResponse(status(true, "Corp AD"))
        coEvery { api.getOidcEnabled() } returns ApiResponse(status(true, "Workspace"))
        coEvery { api.getSamlEnabled() } returns ApiResponse(status(false))
        val repo = AuthRepository(api, mockk(relaxed = true))

        val got = repo.getAuthProviders()
        assertThat(got.ldap?.enabled).isTrue()
        assertThat(got.ldap?.display_name).isEqualTo("Corp AD")
        assertThat(got.oidc?.enabled).isTrue()
        assertThat(got.saml?.enabled).isFalse()
        assertThat(got.ldapEnabled).isTrue()
        assertThat(got.needsBrowserPairing).isTrue()  // OIDC enabled
    }

    @Test
    fun `getAuthProviders maps a failed probe to null without failing the others`() = runTest {
        // Server doesn't have the SAML route wired (older build) → 404.
        // The aggregate should still carry valid LDAP + OIDC values.
        val api = mockk<OnScreenApi>()
        coEvery { api.getLdapEnabled() } returns ApiResponse(status(true))
        coEvery { api.getOidcEnabled() } returns ApiResponse(status(false))
        coEvery { api.getSamlEnabled() } throws RuntimeException("404")
        val repo = AuthRepository(api, mockk(relaxed = true))

        val got = repo.getAuthProviders()
        assertThat(got.ldap?.enabled).isTrue()
        assertThat(got.oidc?.enabled).isFalse()
        assertThat(got.saml).isNull()
        // OIDC explicitly disabled + SAML probe failed → no browser
        // pairing hint.
        assertThat(got.needsBrowserPairing).isFalse()
    }

    @Test
    fun `getAuthProviders returns all-null when the server is unreachable`() = runTest {
        // Worst case — every probe fails. UI falls back to local-only
        // login, both rows hidden.
        val api = mockk<OnScreenApi>()
        coEvery { api.getLdapEnabled() } throws RuntimeException("network")
        coEvery { api.getOidcEnabled() } throws RuntimeException("network")
        coEvery { api.getSamlEnabled() } throws RuntimeException("network")
        val repo = AuthRepository(api, mockk(relaxed = true))

        val got = repo.getAuthProviders()
        assertThat(got.ldap).isNull()
        assertThat(got.oidc).isNull()
        assertThat(got.saml).isNull()
        assertThat(got.ldapEnabled).isFalse()
        assertThat(got.needsBrowserPairing).isFalse()
    }

    @Test
    fun `loginLdap posts to the LDAP endpoint and persists tokens`() = runTest {
        val api = mockk<OnScreenApi>()
        val prefs = mockk<ServerPrefs>(relaxed = true)
        val token = pair()
        coEvery { api.loginLdap(LoginRequest("alice", "pw")) } returns ApiResponse(token)

        val repo = AuthRepository(api, prefs)
        val got = repo.loginLdap("alice", "pw")
        assertThat(got).isEqualTo(token)
        // Side-effect: persist tokens identically to local login so
        // the UI is unchanged downstream.
        io.mockk.coVerify { prefs.setTokens("at", "rt") }
        io.mockk.coVerify { prefs.setUser("u1", "alice") }
    }
}
