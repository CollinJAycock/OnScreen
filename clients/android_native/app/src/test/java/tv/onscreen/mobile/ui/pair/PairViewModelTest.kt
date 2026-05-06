package tv.onscreen.mobile.ui.pair

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.model.PairCodeResponse
import tv.onscreen.mobile.data.model.TokenPair
import tv.onscreen.mobile.data.repository.AuthRepository

@OptIn(ExperimentalCoroutinesApi::class)
class PairViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After  fun tearDown() { Dispatchers.resetMain() }

    private fun tokenPair() = TokenPair(
        access_token = "at", refresh_token = "rt", expires_at = "2026-01-01T00:00:00Z",
        user_id = "u1", username = "user", is_admin = false,
    )

    @Test
    fun `submitServerUrl normalizes naked host and reaches ServerReady when reachable`() = runTest(dispatcher) {
        val auth = mockk<AuthRepository>()
        coEvery { auth.checkServer("https://onscreen.tv") } returns true

        val vm = PairViewModel(auth)
        vm.submitServerUrl("onscreen.tv")
        advanceUntilIdle()

        assertThat(vm.state.value).isEqualTo(PairState.ServerReady)
    }

    @Test
    fun `submitServerUrl unreachable surfaces ServerUnreachable`() = runTest(dispatcher) {
        val auth = mockk<AuthRepository>()
        coEvery { auth.checkServer(any()) } returns false

        val vm = PairViewModel(auth)
        vm.submitServerUrl("https://broken.example")
        advanceUntilIdle()

        assertThat(vm.state.value).isEqualTo(PairState.ServerUnreachable)
    }

    @Test
    fun `submitServerUrl ignores blank input`() = runTest(dispatcher) {
        val auth = mockk<AuthRepository>()
        val vm = PairViewModel(auth)
        vm.submitServerUrl("   ")
        advanceUntilIdle()

        assertThat(vm.state.value).isEqualTo(PairState.NeedsServer)
    }

    @Test
    fun `loginWithPassword success transitions to Done`() = runTest(dispatcher) {
        val auth = mockk<AuthRepository>()
        coEvery { auth.login("u", "p") } returns tokenPair()

        val vm = PairViewModel(auth)
        vm.loginWithPassword("u", "p")
        advanceUntilIdle()

        assertThat(vm.state.value).isEqualTo(PairState.Done)
    }

    @Test
    fun `loginWithPassword failure surfaces Error with message`() = runTest(dispatcher) {
        val auth = mockk<AuthRepository>()
        coEvery { auth.login(any(), any()) } throws RuntimeException("bad credentials")

        val vm = PairViewModel(auth)
        vm.loginWithPassword("u", "p")
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s).isInstanceOf(PairState.Error::class.java)
        assertThat((s as PairState.Error).message).isEqualTo("bad credentials")
    }

    @Test
    fun `startPairing emits WaitingForClaim with PIN then Done after poll resolves`() = runTest(dispatcher) {
        val auth = mockk<AuthRepository>()
        coEvery { auth.startPairing() } returns PairCodeResponse(
            pin = "123456", device_token = "dt", expires_at = "2026-01-01T00:00:00Z",
        )
        coEvery { auth.pollPairing("dt") } returns AuthRepository.PollResult.Done(tokenPair())
        coEvery { auth.completePairing(any()) } returns Unit

        val vm = PairViewModel(auth)
        vm.startPairing()
        advanceUntilIdle()

        assertThat(vm.state.value).isEqualTo(PairState.Done)
    }

    @Test
    fun `pairing code expiry surfaces Error`() = runTest(dispatcher) {
        val auth = mockk<AuthRepository>()
        coEvery { auth.startPairing() } returns PairCodeResponse(
            pin = "999999", device_token = "dt", expires_at = "2026-01-01T00:00:00Z",
        )
        coEvery { auth.pollPairing("dt") } returns AuthRepository.PollResult.Expired

        val vm = PairViewModel(auth)
        vm.startPairing()
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s).isInstanceOf(PairState.Error::class.java)
    }

    @Test
    fun `reset returns to NeedsServer`() = runTest(dispatcher) {
        val auth = mockk<AuthRepository>()
        coEvery { auth.checkServer(any()) } returns true

        val vm = PairViewModel(auth)
        vm.submitServerUrl("https://srv")
        advanceUntilIdle()
        assertThat(vm.state.value).isEqualTo(PairState.ServerReady)

        vm.reset()
        assertThat(vm.state.value).isEqualTo(PairState.NeedsServer)
    }

    // ── SSO bridge ───────────────────────────────────────────────────────

    @Test
    fun `startSsoBridge requests pair code, queues custom-tabs URL, polls`() =
        runTest(dispatcher) {
            val auth = mockk<AuthRepository>()
            coEvery { auth.getServerUrl() } returns "https://server.example"
            coEvery { auth.startPairing() } returns PairCodeResponse(
                pin = "987654",
                device_token = "dev-tok",
                expires_at = "2026-01-01T00:00:00Z",
            )
            // First poll already done — simulates the auto-claim
            // landing immediately after Custom Tabs opens.
            coEvery { auth.pollPairing("dev-tok") } returns
                AuthRepository.PollResult.Done(tokenPair())
            coEvery { auth.completePairing(any()) } returns Unit

            val vm = PairViewModel(auth)
            vm.startSsoBridge(deviceName = "Pixel 8")
            advanceUntilIdle()

            // SSO URL surfaced for the screen to feed Custom Tabs.
            assertThat(vm.ssoLaunchUrl.value).isEqualTo(
                "https://server.example/pair?code=987654&auto=1&device_name=Pixel+8",
            )
            // Polling resolved → Done.
            assertThat(vm.state.value).isEqualTo(PairState.Done)
        }

    @Test
    fun `startSsoBridge surfaces Error when server URL is unset`() =
        runTest(dispatcher) {
            val auth = mockk<AuthRepository>()
            coEvery { auth.getServerUrl() } returns null

            val vm = PairViewModel(auth)
            vm.startSsoBridge()
            advanceUntilIdle()

            assertThat(vm.state.value).isInstanceOf(PairState.Error::class.java)
            assertThat(vm.ssoLaunchUrl.value).isNull()
        }

    @Test
    fun `startSsoBridge refuses non-http server URLs`() = runTest(dispatcher) {
        // Defensive — server URL came from an earlier PairScreen
        // submission so should already be http(s), but guard against
        // a corrupted prefs blob (e.g. a downgraded build wrote
        // something weird).
        val auth = mockk<AuthRepository>()
        coEvery { auth.getServerUrl() } returns "file:///etc/hosts"

        val vm = PairViewModel(auth)
        vm.startSsoBridge()
        advanceUntilIdle()

        assertThat(vm.state.value).isInstanceOf(PairState.Error::class.java)
    }

    @Test
    fun `consumeSsoLaunchUrl clears the one-shot URL`() = runTest(dispatcher) {
        val auth = mockk<AuthRepository>()
        coEvery { auth.getServerUrl() } returns "https://server.example"
        coEvery { auth.startPairing() } returns PairCodeResponse(
            pin = "111111", device_token = "tok", expires_at = "2026-01-01T00:00:00Z",
        )
        coEvery { auth.pollPairing(any()) } returns AuthRepository.PollResult.Pending

        val vm = PairViewModel(auth)
        vm.startSsoBridge()
        advanceUntilIdle()
        assertThat(vm.ssoLaunchUrl.value).isNotNull()
        vm.consumeSsoLaunchUrl()
        assertThat(vm.ssoLaunchUrl.value).isNull()
    }
}
