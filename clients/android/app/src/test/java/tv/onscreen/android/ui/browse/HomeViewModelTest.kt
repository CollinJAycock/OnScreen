package tv.onscreen.android.ui.browse

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.just
import io.mockk.mockk
import io.mockk.Runs
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import kotlinx.coroutines.launch
import okhttp3.ResponseBody.Companion.toResponseBody
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.android.data.model.HubCollectionRow
import tv.onscreen.android.data.model.HubData
import tv.onscreen.android.data.model.HubItem
import tv.onscreen.android.data.model.HubRowPref
import tv.onscreen.android.data.model.Library
import tv.onscreen.android.data.model.MediaCollection
import tv.onscreen.android.data.model.MediaItem
import tv.onscreen.android.data.model.UserPreferences
import tv.onscreen.android.data.repository.CollectionRepository
import tv.onscreen.android.data.repository.HubRepository
import tv.onscreen.android.data.repository.LibraryRepository
import tv.onscreen.android.data.repository.PreferencesRepository

@OptIn(ExperimentalCoroutinesApi::class)
class HomeViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After  fun tearDown() { Dispatchers.resetMain() }

    private fun hub(id: String, type: String = "movie") =
        HubItem(id = id, title = "t-$id", type = type)
    private fun item(id: String) = MediaItem(
        id = id, title = "t-$id", type = "movie",
        created_at = "2026-01-01T00:00:00Z", updated_at = "2026-01-01T00:00:00Z",
    )
    private fun lib(id: String) = Library(
        id = id, name = "L-$id", type = "movies",
        created_at = "2026-01-01T00:00:00Z", updated_at = "2026-01-01T00:00:00Z",
    )

    private fun mocks(
        hubData: HubData = HubData(),
        libraries: List<Library> = emptyList(),
        collections: List<MediaCollection> = emptyList(),
        hubLayout: List<HubRowPref>? = null,
    ): Mocks {
        val hubRepo = mockk<HubRepository>()
        val libRepo = mockk<LibraryRepository>()
        val colRepo = mockk<CollectionRepository>()
        val prefRepo = mockk<PreferencesRepository>()
        coEvery { hubRepo.getHub() } returns hubData
        coEvery { libRepo.getLibraries() } returns libraries
        coEvery { colRepo.getCollections() } returns collections
        coEvery { prefRepo.get() } returns UserPreferences(hub_layout = hubLayout)
        libraries.forEach { l ->
            coEvery { libRepo.getItems(l.id, limit = 20) } returns (emptyList<MediaItem>() to 0)
        }
        return Mocks(hubRepo, libRepo, colRepo, prefRepo)
    }

    private data class Mocks(
        val hub: HubRepository,
        val lib: LibraryRepository,
        val col: CollectionRepository,
        val pref: PreferencesRepository,
    )

    @Test
    fun `load composes hub + recently added + library previews`() = runTest(dispatcher) {
        val m = mocks(
            hubData = HubData(
                continue_watching = listOf(hub("cw1", type = "movie"), hub("cw2", type = "episode")),
                recently_added = listOf(hub("ra1"), hub("ra2")),
            ),
            libraries = listOf(lib("l1")),
        )
        coEvery { m.lib.getItems("l1", limit = 20) } returns (listOf(item("a"), item("b")) to 2)

        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.isLoading).isFalse()
        assertThat(state.continueWatchingMovies.map { it.id }).containsExactly("cw1")
        assertThat(state.continueWatchingTV.map { it.id }).containsExactly("cw2")
        assertThat(state.recentlyAdded.map { it.id }).containsExactly("ra1", "ra2").inOrder()
        assertThat(state.libraryPreviews).hasSize(1)
        assertThat(state.libraryPreviews[0].second.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(state.error).isNull()
    }

    @Test
    fun `saved hub layout flows into state`() = runTest(dispatcher) {
        val layout = listOf(
            HubRowPref("trending", enabled = true),
            HubRowPref("continue_tv", enabled = false),
        )
        val m = mocks(hubLayout = layout)

        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()

        assertThat(vm.uiState.value.hubLayout).isEqualTo(layout)
    }

    @Test
    fun `a prefs failure falls back to the default empty layout`() = runTest(dispatcher) {
        val m = mocks()
        coEvery { m.pref.get() } throws RuntimeException("prefs down")

        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()

        // Home still loads; layout falls back to default (empty).
        assertThat(vm.uiState.value.error).isNull()
        assertThat(vm.uiState.value.hubLayout).isEmpty()
    }

    @Test
    fun `load records error when hub repo throws`() = runTest(dispatcher) {
        val m = mocks()
        coEvery { m.hub.getHub() } throws RuntimeException("offline")

        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()

        assertThat(vm.uiState.value.error).isEqualTo("offline")
        assertThat(vm.uiState.value.isLoading).isFalse()
    }

    @Test
    fun `library preview failure leaves the row empty without failing the whole load`() = runTest(dispatcher) {
        val m = mocks(libraries = listOf(lib("l1"), lib("l2")))
        coEvery { m.lib.getItems("l1", limit = 20) } returns (listOf(item("a")) to 1)
        coEvery { m.lib.getItems("l2", limit = 20) } throws RuntimeException("lib2 down")

        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.error).isNull()
        val byId = state.libraryPreviews.associate { it.first.id to it.second }
        assertThat(byId["l1"]!!.map { it.id }).containsExactly("a")
        assertThat(byId["l2"]).isEmpty()
    }

    @Test
    fun `Continue Watching split prefers server-side fields when present`() = runTest(dispatcher) {
        // Newer server: pre-split arrays populated. The
        // client-side filter on continue_watching is skipped, so the
        // legacy combined feed can stay empty without affecting
        // what the UI renders.
        val m = mocks(
            hubData = HubData(
                continue_watching = emptyList(),
                continue_watching_tv = listOf(hub("ep1", type = "episode")),
                continue_watching_movies = listOf(hub("mv1", type = "movie")),
                continue_watching_other = listOf(hub("ab1", type = "audiobook")),
            ),
        )
        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()
        val state = vm.uiState.value
        assertThat(state.continueWatchingTV.map { it.id }).containsExactly("ep1")
        assertThat(state.continueWatchingMovies.map { it.id }).containsExactly("mv1")
        assertThat(state.continueWatchingOther.map { it.id }).containsExactly("ab1")
    }
    // ── v2.5 watch rows ─────────────────────────────────────────────────────

    @Test
    fun `next up and plan to watch flow into state`() = runTest(dispatcher) {
        val m = mocks(
            hubData = HubData(
                next_up = listOf(
                    HubItem(
                        id = "e5", title = "Five", type = "episode",
                        show_id = "s1", show_title = "Show", season_number = 2, episode_number = 5,
                    ),
                ),
                plan_to_watch = listOf(hub("p1"), hub("p2", type = "show")),
            ),
        )
        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.nextUp.map { it.id }).containsExactly("e5")
        assertThat(state.nextUp[0].show_title).isEqualTo("Show")
        assertThat(state.planToWatch.map { it.id }).containsExactly("p1", "p2").inOrder()
        // Either row alone counts as content (no empty-state rows).
        assertThat(state.hasContent).isTrue()
    }

    @Test
    fun `older servers without the watch rows leave them empty`() = runTest(dispatcher) {
        val m = mocks(hubData = HubData(trending = listOf(hub("t1"))))
        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()

        assertThat(vm.uiState.value.nextUp).isEmpty()
        assertThat(vm.uiState.value.planToWatch).isEmpty()
        assertThat(vm.uiState.value.collectionRows).isEmpty()
    }

    @Test
    fun `promoted collection rows flow into state and count as content`() = runTest(dispatcher) {
        val m = mocks(
            hubData = HubData(
                collection_rows = listOf(
                    HubCollectionRow(collection_id = "c1", name = "Spooky Season", items = listOf(hub("m1"))),
                    HubCollectionRow(collection_id = "c2", name = "Empty", items = emptyList()),
                ),
            ),
        )
        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.collectionRows.map { it.collection_id }).containsExactly("c1", "c2").inOrder()
        assertThat(state.collectionRows[0].items.map { it.id }).containsExactly("m1")
        assertThat(state.hasContent).isTrue()
    }

    // ── Continue Watching dismiss ───────────────────────────────────────────

    private fun cwHub() = HubData(
        continue_watching_tv = listOf(hub("show1", type = "show"), hub("show2", type = "show")),
        continue_watching_movies = listOf(hub("mv1"), hub("mv2"), hub("mv3")),
        continue_watching_other = emptyList(),
    )

    @Test
    fun `dismiss removes the tile immediately and calls the server`() = runTest(dispatcher) {
        val m = mocks(hubData = cwHub())
        val gate = CompletableDeferred<Unit>()
        coEvery { m.hub.dismissContinueWatching("mv2") } coAnswers { gate.await() }
        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()
        val events = mutableListOf<HomeEvent>()
        val job = backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { vm.events.collect { events.add(it) } }

        vm.dismissContinueWatching(hub("mv2"))
        // Optimistic: gone before the server answers.
        assertThat(vm.uiState.value.continueWatchingMovies.map { it.id }).containsExactly("mv1", "mv3").inOrder()

        gate.complete(Unit)
        advanceUntilIdle()
        assertThat(vm.uiState.value.continueWatchingMovies.map { it.id }).containsExactly("mv1", "mv3").inOrder()
        assertThat(vm.uiState.value.continueWatchingTV.map { it.id }).containsExactly("show1", "show2").inOrder()
        coVerify(exactly = 1) { m.hub.dismissContinueWatching("mv2") }
        // Success is reported so the fragment can clear the launcher's Watch Next row.
        assertThat(events).containsExactly(HomeEvent.Dismissed("mv2"))
        job.cancel()
    }

    @Test
    fun `dismiss failure restores the tile at its old position and reports it`() = runTest(dispatcher) {
        val m = mocks(hubData = cwHub())
        coEvery { m.hub.dismissContinueWatching("mv2") } throws RuntimeException("offline")
        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()

        val events = mutableListOf<HomeEvent>()
        val job = backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { vm.events.collect { events.add(it) } }

        vm.dismissContinueWatching(hub("mv2"))
        advanceUntilIdle()

        assertThat(vm.uiState.value.continueWatchingMovies.map { it.id })
            .containsExactly("mv1", "mv2", "mv3").inOrder()
        assertThat(events).containsExactly(HomeEvent.DismissFailed(rateLimited = false))
        job.cancel()
    }

    @Test
    fun `a rate-limited dismiss is reported as such`() = runTest(dispatcher) {
        val m = mocks(hubData = cwHub())
        coEvery { m.hub.dismissContinueWatching("show1") } throws
            HttpException(Response.error<Any>(429, "".toResponseBody(null)))
        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()

        val events = mutableListOf<HomeEvent>()
        val job = backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { vm.events.collect { events.add(it) } }

        vm.dismissContinueWatching(hub("show1", type = "show"))
        advanceUntilIdle()

        assertThat(vm.uiState.value.continueWatchingTV.map { it.id }).containsExactly("show1", "show2").inOrder()
        assertThat(events).containsExactly(HomeEvent.DismissFailed(rateLimited = true))
        job.cancel()
    }

    @Test
    fun `a refresh landing while a dismiss is in flight does not bring the tile back`() = runTest(dispatcher) {
        val m = mocks(hubData = cwHub())
        val gate = CompletableDeferred<Unit>()
        coEvery { m.hub.dismissContinueWatching("mv1") } coAnswers { gate.await() }
        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()

        vm.dismissContinueWatching(hub("mv1"))
        vm.load() // onResume refresh; the hub was read before the dismissal committed
        advanceUntilIdle()
        assertThat(vm.uiState.value.continueWatchingMovies.map { it.id }).containsExactly("mv2", "mv3").inOrder()

        gate.complete(Unit)
        advanceUntilIdle()
        assertThat(vm.uiState.value.continueWatchingMovies.map { it.id }).containsExactly("mv2", "mv3").inOrder()
    }

    @Test
    fun `a refresh that started before the dismissal cannot bring the tile back after it commits`() =
        runTest(dispatcher) {
            val m = mocks(hubData = cwHub())
            coEvery { m.hub.dismissContinueWatching("mv1") } just Runs
            val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
            vm.load()
            advanceUntilIdle()

            // onResume refresh: its hub read predates the dismissal (still has mv1).
            val staleRead = CompletableDeferred<HubData>()
            coEvery { m.hub.getHub() } coAnswers { staleRead.await() }
            vm.load()
            advanceUntilIdle()

            // The user removes the tile; the server commits while that read is out.
            vm.dismissContinueWatching(hub("mv1"))
            advanceUntilIdle()
            staleRead.complete(cwHub())
            advanceUntilIdle()
            assertThat(vm.uiState.value.continueWatchingMovies.map { it.id }).containsExactly("mv2", "mv3").inOrder()

            // A load that started after the commit is authoritative: the server
            // no longer lists it...
            coEvery { m.hub.getHub() } returns cwHub().copy(continue_watching_movies = listOf(hub("mv2"), hub("mv3")))
            vm.load()
            advanceUntilIdle()
            assertThat(vm.uiState.value.continueWatchingMovies.map { it.id }).containsExactly("mv2", "mv3").inOrder()
            // ...and once it lists it again (watched again later), the tile is back.
            coEvery { m.hub.getHub() } returns cwHub()
            vm.load()
            advanceUntilIdle()
            assertThat(vm.uiState.value.continueWatchingMovies.map { it.id })
                .containsExactly("mv1", "mv2", "mv3").inOrder()
        }

    @Test
    fun `the optimistic removal renders even while a refresh is loading`() = runTest(dispatcher) {
        val m = mocks(hubData = cwHub())
        val gate = CompletableDeferred<Unit>()
        coEvery { m.hub.dismissContinueWatching("mv2") } coAnswers { gate.await() }
        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()

        val slowRead = CompletableDeferred<HubData>()
        coEvery { m.hub.getHub() } coAnswers { slowRead.await() }
        vm.load()
        advanceUntilIdle()
        assertThat(vm.uiState.value.isLoading).isTrue()

        vm.dismissContinueWatching(hub("mv2"))
        // HomeFragment skips isLoading states — this one must be drawn now.
        assertThat(vm.uiState.value.isLoading).isFalse()
        assertThat(vm.uiState.value.continueWatchingMovies.map { it.id }).containsExactly("mv1", "mv3").inOrder()

        slowRead.complete(cwHub())
        gate.complete(Unit)
        advanceUntilIdle()
        assertThat(vm.uiState.value.continueWatchingMovies.map { it.id }).containsExactly("mv1", "mv3").inOrder()
    }

    @Test
    fun `a second dismiss of the same tile while one is in flight is ignored`() = runTest(dispatcher) {
        val m = mocks(hubData = cwHub())
        coEvery { m.hub.dismissContinueWatching("mv1") } just Runs
        val vm = HomeViewModel(m.hub, m.lib, m.col, m.pref)
        vm.load()
        advanceUntilIdle()

        vm.dismissContinueWatching(hub("mv1"))
        vm.dismissContinueWatching(hub("mv1"))
        advanceUntilIdle()

        coVerify(exactly = 1) { m.hub.dismissContinueWatching("mv1") }
    }

    @Test
    fun `restoreAt keeps an item a refresh already re-added`() {
        val list = listOf(hub("a"), hub("b"))
        val restored = HomeViewModel.restoreAt(list, IndexedValue(0, hub("b")))
        assertThat(restored.map { it.id }).containsExactly("a", "b").inOrder()
        // Index past the end clamps to the end.
        assertThat(HomeViewModel.restoreAt(list, IndexedValue(9, hub("c"))).map { it.id })
            .containsExactly("a", "b", "c").inOrder()
    }
}
