package tv.onscreen.mobile.ui.hub

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
import tv.onscreen.mobile.data.model.HubData
import tv.onscreen.mobile.data.model.HubItem
import tv.onscreen.mobile.data.model.Library
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.HubRepository
import tv.onscreen.mobile.data.repository.LibraryRepository

@OptIn(ExperimentalCoroutinesApi::class)
class HubViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After  fun tearDown() { Dispatchers.resetMain() }

    private fun lib(id: String) = Library(
        id = id, name = "L-$id", type = "movies",
        created_at = "2026-01-01T00:00:00Z", updated_at = "2026-01-01T00:00:00Z",
    )

    @Test
    fun `init triggers load and populates state from repos`() = runTest(dispatcher) {
        val hub = mockk<HubRepository>()
        val libs = mockk<LibraryRepository>()
        val prefs = mockk<ServerPrefs>(relaxed = true)
        val hubData = HubData(
            recently_added = listOf(HubItem(id = "ra1", title = "t", type = "movie")),
        )
        coEvery { hub.getHub() } returns hubData
        coEvery { libs.getLibraries() } returns listOf(lib("l1"), lib("l2"))
        coEvery { prefs.getServerUrl() } returns "http://srv"

        val vm = HubViewModel(hub, libs, prefs)
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.loading).isFalse()
        assertThat(s.error).isNull()
        assertThat(s.hub).isEqualTo(hubData)
        assertThat(s.libraries.map { it.id }).containsExactly("l1", "l2").inOrder()
        assertThat(s.serverUrl).isEqualTo("http://srv")
    }

    @Test
    fun `hub repo failure surfaces error message and clears loading`() = runTest(dispatcher) {
        val hub = mockk<HubRepository>()
        val libs = mockk<LibraryRepository>()
        val prefs = mockk<ServerPrefs>(relaxed = true)
        coEvery { hub.getHub() } throws RuntimeException("offline")
        coEvery { libs.getLibraries() } returns emptyList()

        val vm = HubViewModel(hub, libs, prefs)
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.loading).isFalse()
        assertThat(s.error).isEqualTo("offline")
        assertThat(s.hub).isNull()
    }

    @Test
    fun `null serverUrl from prefs collapses to empty string`() = runTest(dispatcher) {
        // Older installs may not have a server URL set; the UI uses
        // serverUrl as a base for asset URLs and can't tolerate null.
        val hub = mockk<HubRepository>()
        val libs = mockk<LibraryRepository>()
        val prefs = mockk<ServerPrefs>(relaxed = true)
        coEvery { hub.getHub() } returns HubData()
        coEvery { libs.getLibraries() } returns emptyList()
        coEvery { prefs.getServerUrl() } returns null

        val vm = HubViewModel(hub, libs, prefs)
        advanceUntilIdle()

        assertThat(vm.state.value.serverUrl).isEqualTo("")
    }
}
