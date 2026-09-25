package tv.onscreen.mobile.playback

import android.content.Context
import com.google.common.truth.Truth.assertThat
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.AuthRepository

/**
 * The involuntary sign-out path: TokenAuthenticator only clears prefs, and
 * SignOutTeardown has to turn the resulting logged-in → logged-out edge into
 * the same device teardown a voluntary sign-out performs.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class SignOutTeardownTest {

    private val dispatcher = StandardTestDispatcher()
    private val scope = TestScope(dispatcher)

    private val loggedIn = MutableStateFlow(true)
    private val prefs = mockk<ServerPrefs>().also { every { it.isLoggedIn } returns loggedIn }
    private val context = mockk<Context>(relaxed = true)
    private val authRepo = mockk<AuthRepository>(relaxed = true)

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() {
        Dispatchers.resetMain()
        StreamTokenVault.clear()
    }

    private fun started() = SignOutTeardown(context, prefs, authRepo).also {
        it.start()
        scope.runCurrent()
    }

    @Test
    fun `logged-in to logged-out edge stops audio and drops credentials and caches`() {
        started()
        val url = StreamTokenVault.register("http://srv/media/stream/f1", "revoked-user-token")

        // TokenAuthenticator's definitive-rejection path: prefs.clearAuth().
        loggedIn.value = false
        scope.runCurrent()

        verify(exactly = 1) { context.stopService(any()) }
        verify(exactly = 1) { authRepo.onInvoluntarySignOut() }
        assertThat(StreamTokenVault.tokenForTest(url)).isNull()
    }

    @Test
    fun `starting signed out does not tear down, and neither does signing in`() {
        loggedIn.value = false
        started()

        loggedIn.value = true
        scope.runCurrent()

        verify(exactly = 0) { context.stopService(any()) }
        verify(exactly = 0) { authRepo.onInvoluntarySignOut() }
    }

    @Test
    fun `each sign-out edge fires once, and start is idempotent`() {
        val teardown = started()
        teardown.start() // second call must not add a second watcher
        scope.runCurrent()

        loggedIn.value = false
        scope.runCurrent()
        loggedIn.value = true
        scope.runCurrent()
        loggedIn.value = false
        scope.runCurrent()

        verify(exactly = 2) { context.stopService(any()) }
        verify(exactly = 2) { authRepo.onInvoluntarySignOut() }
    }

    @Test
    fun `a failing step does not stop the rest of the teardown`() {
        every { context.stopService(any()) } throws SecurityException("boom")
        started()
        val url = StreamTokenVault.register("http://srv/media/stream/f2", "tok")

        loggedIn.value = false
        scope.runCurrent()

        verify(exactly = 1) { authRepo.onInvoluntarySignOut() }
        assertThat(StreamTokenVault.tokenForTest(url)).isNull()
    }
}
