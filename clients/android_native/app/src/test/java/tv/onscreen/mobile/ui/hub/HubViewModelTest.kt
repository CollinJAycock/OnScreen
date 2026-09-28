package tv.onscreen.mobile.ui.hub

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.CompletableDeferred
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
import tv.onscreen.mobile.data.model.HubRowPref
import tv.onscreen.mobile.data.model.Library
import tv.onscreen.mobile.data.model.UserPreferences
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.HubRepository
import tv.onscreen.mobile.data.repository.LibraryRepository
import tv.onscreen.mobile.data.repository.PreferencesRepository

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
        val prefsRepo = mockk<PreferencesRepository>()
        coEvery { prefsRepo.get() } returns UserPreferences()
        val hubData = HubData(
            recently_added = listOf(HubItem(id = "ra1", title = "t", type = "movie")),
        )
        coEvery { hub.getHub() } returns hubData
        coEvery { libs.getLibraries() } returns listOf(lib("l1"), lib("l2"))
        coEvery { prefs.getServerUrl() } returns "http://srv"

        val vm = HubViewModel(hub, libs, prefs, prefsRepo)
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
        val prefsRepo = mockk<PreferencesRepository>()
        coEvery { prefsRepo.get() } returns UserPreferences()
        coEvery { hub.getHub() } throws RuntimeException("offline")
        coEvery { libs.getLibraries() } returns emptyList()

        val vm = HubViewModel(hub, libs, prefs, prefsRepo)
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
        val prefsRepo = mockk<PreferencesRepository>()
        coEvery { prefsRepo.get() } returns UserPreferences()
        coEvery { hub.getHub() } returns HubData()
        coEvery { libs.getLibraries() } returns emptyList()
        coEvery { prefs.getServerUrl() } returns null

        val vm = HubViewModel(hub, libs, prefs, prefsRepo)
        advanceUntilIdle()

        assertThat(vm.state.value.serverUrl).isEqualTo("")
    }

    @Test
    fun `saved hub layout flows into state`() = runTest(dispatcher) {
        val hub = mockk<HubRepository>()
        val libs = mockk<LibraryRepository>()
        val prefs = mockk<ServerPrefs>(relaxed = true)
        val prefsRepo = mockk<PreferencesRepository>()
        val layout = listOf(
            HubRowPref("trending", enabled = true),
            HubRowPref("continue_tv", enabled = false),
        )
        coEvery { hub.getHub() } returns HubData()
        coEvery { libs.getLibraries() } returns emptyList()
        coEvery { prefsRepo.get() } returns UserPreferences(hub_layout = layout)

        val vm = HubViewModel(hub, libs, prefs, prefsRepo)
        advanceUntilIdle()

        assertThat(vm.state.value.hubLayout).isEqualTo(layout)
    }

    @Test
    fun `a prefs failure falls back to the default empty layout`() = runTest(dispatcher) {
        val hub = mockk<HubRepository>()
        val libs = mockk<LibraryRepository>()
        val prefs = mockk<ServerPrefs>(relaxed = true)
        val prefsRepo = mockk<PreferencesRepository>()
        coEvery { hub.getHub() } returns HubData()
        coEvery { libs.getLibraries() } returns emptyList()
        coEvery { prefsRepo.get() } throws RuntimeException("prefs down")

        val vm = HubViewModel(hub, libs, prefs, prefsRepo)
        advanceUntilIdle()

        // Home still loads; layout falls back to default (empty).
        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.hubLayout).isEmpty()
    }

    // ── Continue Watching dismiss ──────────────────────────────────────────

    private fun hi(id: String, type: String = "movie") = HubItem(id = id, title = id, type = type)

    private fun cwHub() = HubData(
        continue_watching = listOf(hi("show1", "show"), hi("m1"), hi("m2")),
        continue_watching_tv = listOf(hi("show1", "show")),
        continue_watching_movies = listOf(hi("m1"), hi("m2")),
        continue_watching_other = emptyList(),
        next_up = listOf(hi("ep9", "episode")),
    )

    private fun vmWith(hub: HubRepository): HubViewModel {
        val libs = mockk<LibraryRepository>()
        val prefs = mockk<ServerPrefs>(relaxed = true)
        val prefsRepo = mockk<PreferencesRepository>()
        coEvery { libs.getLibraries() } returns emptyList()
        coEvery { prefsRepo.get() } returns UserPreferences()
        return HubViewModel(hub, libs, prefs, prefsRepo)
    }

    @Test
    fun `dismiss removes the tile from every continue list at once`() = runTest(dispatcher) {
        val hub = mockk<HubRepository>()
        coEvery { hub.getHub() } returns cwHub()
        val gate = CompletableDeferred<Unit>()
        coEvery { hub.dismissContinueWatching("m1") } coAnswers { gate.await() }
        val vm = vmWith(hub)
        advanceUntilIdle()

        vm.dismissContinueWatching(hi("m1"))
        // Optimistic: gone before the server answers.
        val during = vm.state.value.hub!!
        assertThat(during.continue_watching.map { it.id }).containsExactly("show1", "m2").inOrder()
        assertThat(during.continue_watching_movies!!.map { it.id }).containsExactly("m2")
        assertThat(during.continue_watching_tv!!.map { it.id }).containsExactly("show1")
        // Next Up is not a Continue Watching row.
        assertThat(during.next_up.map { it.id }).containsExactly("ep9")

        gate.complete(Unit)
        advanceUntilIdle()
        assertThat(vm.state.value.hub!!.continue_watching_movies!!.map { it.id }).containsExactly("m2")
        assertThat(vm.state.value.message).isNull()
        coVerify(exactly = 1) { hub.dismissContinueWatching("m1") }
    }

    @Test
    fun `dismiss failure restores the tile in place and posts a message`() = runTest(dispatcher) {
        val hub = mockk<HubRepository>()
        coEvery { hub.getHub() } returns cwHub()
        coEvery { hub.dismissContinueWatching("m1") } throws RuntimeException("boom")
        val vm = vmWith(hub)
        advanceUntilIdle()

        vm.dismissContinueWatching(hi("m1"))
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.hub!!.continue_watching.map { it.id }).containsExactly("show1", "m1", "m2").inOrder()
        assertThat(s.hub!!.continue_watching_movies!!.map { it.id }).containsExactly("m1", "m2").inOrder()
        assertThat(s.message).isEqualTo("Couldn't remove from Continue Watching")

        vm.consumeMessage()
        assertThat(vm.state.value.message).isNull()
    }

    @Test
    fun `a second dismiss of the same tile while one is in flight is dropped`() = runTest(dispatcher) {
        val hub = mockk<HubRepository>()
        coEvery { hub.getHub() } returns cwHub()
        val gate = CompletableDeferred<Unit>()
        coEvery { hub.dismissContinueWatching("show1") } coAnswers { gate.await() }
        val vm = vmWith(hub)
        advanceUntilIdle()

        vm.dismissContinueWatching(hi("show1", "show"))
        vm.dismissContinueWatching(hi("show1", "show"))
        gate.complete(Unit)
        advanceUntilIdle()

        coVerify(exactly = 1) { hub.dismissContinueWatching("show1") }
        assertThat(vm.state.value.hub!!.continue_watching_tv).isEmpty()
    }

    @Test
    fun `restore does not duplicate a tile a refresh already brought back`() {
        val hub = cwHub()
        val removal = ContinueWatchingRemoval.remove(hub, "m1")
        // A refresh landed meanwhile with m1 still present server-side.
        val restored = removal.restoreInto(hub)
        assertThat(restored.continue_watching_movies!!.map { it.id }).containsExactly("m1", "m2").inOrder()
        assertThat(restored.continue_watching.count { it.id == "m1" }).isEqualTo(1)
    }

    @Test
    fun `legacy server without split rows - dismiss works on the combined feed`() {
        val legacy = HubData(continue_watching = listOf(hi("a"), hi("b")))
        val removal = ContinueWatchingRemoval.remove(legacy, "a")
        assertThat(removal.hub.continue_watching.map { it.id }).containsExactly("b")
        assertThat(removal.hub.continue_watching_tv).isNull()
        val back = removal.restoreInto(removal.hub)
        assertThat(back.continue_watching.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(back.continue_watching_movies).isNull()
    }

    @Test
    fun `quiet refresh re-pulls the hub without the spinner or an error`() = runTest(dispatcher) {
        val hub = mockk<HubRepository>()
        coEvery { hub.getHub() } returns HubData() andThen cwHub() andThenThrows RuntimeException("offline")
        val vm = vmWith(hub)
        advanceUntilIdle()
        assertThat(vm.state.value.hub!!.next_up).isEmpty()

        vm.refreshQuietly()
        assertThat(vm.state.value.loading).isFalse()
        advanceUntilIdle()
        assertThat(vm.state.value.hub!!.next_up.map { it.id }).containsExactly("ep9")

        // A failing quiet refresh keeps the rows and shows no error.
        vm.refreshQuietly()
        advanceUntilIdle()
        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.hub!!.next_up.map { it.id }).containsExactly("ep9")
    }
}
