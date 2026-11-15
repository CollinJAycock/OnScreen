package tv.onscreen.android.data.prefs

import android.content.Context
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.Before
import org.junit.Test
import java.io.File
import java.nio.file.Files

/**
 * ServerPrefs over a real DataStore file (the Context is a stub pointing it at
 * a temp directory). AndroidKeyStore doesn't exist on the JVM, so TokenCrypto
 * falls back to storing the tokens as written — the compare logic is the same.
 */
class ServerPrefsAuthTest {

    private lateinit var prefs: ServerPrefs

    @Before
    fun setUp() {
        val ctx = mockk<Context>(relaxed = true)
        every { ctx.applicationContext } returns ctx
        every { ctx.filesDir } returns filesDir
        prefs = ServerPrefs(ctx)
        runBlocking { prefs.clearAll() }
    }

    @Test
    fun `the no-refresh-token clear leaves a sign-in stored meanwhile alone`() = runBlocking {
        // TokenAuthenticator read no refresh token, then a sign-in stored its
        // pair before the clear ran.
        prefs.setTokens("fresh-access", "fresh-refresh")
        prefs.setUser("u1", "alice")

        assertThat(prefs.clearAuthIfRefreshTokenIs(null)).isFalse()

        assertThat(prefs.getAccessToken()).isEqualTo("fresh-access")
        assertThat(prefs.getRefreshToken()).isEqualTo("fresh-refresh")
        assertThat(prefs.isLoggedIn.first()).isTrue()
    }

    @Test
    fun `a rejected refresh token clears only while it is still the stored one`() = runBlocking {
        prefs.setTokens("new-access", "new-refresh")

        // The authenticator's refresh used the previous token: not ours to clear.
        assertThat(prefs.clearAuthIfRefreshTokenIs("old-refresh")).isFalse()
        assertThat(prefs.isLoggedIn.first()).isTrue()

        assertThat(prefs.clearAuthIfRefreshTokenIs("new-refresh")).isTrue()
        assertThat(prefs.getAccessToken()).isNull()
        assertThat(prefs.getRefreshToken()).isNull()
        assertThat(prefs.isLoggedIn.first()).isFalse()
    }

    @Test
    fun `with still no refresh token stored the no-refresh-token clear signs out`() = runBlocking {
        prefs.setUser("u1", "alice")

        assertThat(prefs.clearAuthIfRefreshTokenIs(null)).isTrue()
        assertThat(prefs.userId.first()).isNull()
    }

    private companion object {
        /** One directory for the run: the DataStore delegate is a process-wide
         *  singleton bound to the first Context's file. */
        val filesDir: File by lazy { Files.createTempDirectory("serverprefs-test").toFile() }
    }
}
