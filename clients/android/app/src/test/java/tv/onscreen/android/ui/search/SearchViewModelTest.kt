package tv.onscreen.android.ui.search

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import kotlinx.coroutines.flow.MutableStateFlow
import tv.onscreen.android.data.model.Library
import tv.onscreen.android.data.model.SearchResult
import tv.onscreen.android.data.prefs.SearchFilters
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.DiscoverRepository
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.LibraryRepository

@OptIn(ExperimentalCoroutinesApi::class)
class SearchViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() { Dispatchers.setMain(dispatcher) }

    @After
    fun tearDown() { Dispatchers.resetMain() }

    /** Minimal stubs for the dependencies the tests don't exercise.
     *  DiscoverRepository defaults to empty results; ServerPrefs
     *  exposes the all-types-on filter set so the visibleResults
     *  pass-through doesn't mask test assertions. */
    private fun stubDiscover(): DiscoverRepository = mockk(relaxed = true)
    private fun stubPrefs(): ServerPrefs = mockk<ServerPrefs>().also { p ->
        io.mockk.every { p.searchFilters } returns MutableStateFlow(
            SearchFilters(movie = true, show = true, episode = true, track = true),
        )
    }

    private fun lib(id: String, name: String) = Library(
        id = id, name = name, type = "movies",
        created_at = "2026-01-01T00:00:00Z", updated_at = "2026-01-01T00:00:00Z",
    )

    private fun result(id: String) = SearchResult(
        id = id, library_id = "lib", title = "t-$id", type = "movie",
    )

    @Test
    fun `init populates libraries`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val libRepo = mockk<LibraryRepository>()
        coEvery { libRepo.getLibraries() } returns listOf(lib("l1", "Movies"), lib("l2", "Shows"))

        val vm = SearchViewModel(itemRepo, libRepo, stubDiscover(), stubPrefs())
        advanceUntilIdle()

        assertThat(vm.libraries.value.map { it.id }).containsExactly("l1", "l2").inOrder()
    }

    @Test
    fun `search below 2 chars clears results without calling repo`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val libRepo = mockk<LibraryRepository>()
        coEvery { libRepo.getLibraries() } returns emptyList()

        val vm = SearchViewModel(itemRepo, libRepo, stubDiscover(), stubPrefs())
        advanceUntilIdle()

        vm.search("a")
        advanceUntilIdle()

        assertThat(vm.results.value).isEmpty()
        coVerify(exactly = 0) { itemRepo.search(any(), any(), any()) }
    }

    @Test
    fun `search debounces and forwards results`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val libRepo = mockk<LibraryRepository>()
        coEvery { libRepo.getLibraries() } returns emptyList()
        coEvery { itemRepo.search("star", any(), null) } returns listOf(result("x"))

        val vm = SearchViewModel(itemRepo, libRepo, stubDiscover(), stubPrefs())
        advanceUntilIdle()

        vm.search("star")
        // Debounce is 300ms — nothing should have fired yet.
        advanceTimeBy(200)
        coVerify(exactly = 0) { itemRepo.search(any(), any(), any()) }

        advanceUntilIdle()
        assertThat(vm.results.value.map { it.id }).containsExactly("x")
    }

    @Test
    fun `rapid search cancels prior debounced job`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val libRepo = mockk<LibraryRepository>()
        coEvery { libRepo.getLibraries() } returns emptyList()
        coEvery { itemRepo.search("st", any(), null) } returns listOf(result("stale"))
        coEvery { itemRepo.search("star", any(), null) } returns listOf(result("fresh"))

        val vm = SearchViewModel(itemRepo, libRepo, stubDiscover(), stubPrefs())
        advanceUntilIdle()

        vm.search("st")
        advanceTimeBy(100) // Below debounce.
        vm.search("star")
        advanceUntilIdle()

        assertThat(vm.results.value.map { it.id }).containsExactly("fresh")
        coVerify(exactly = 0) { itemRepo.search("st", any(), null) }
    }

    @Test
    fun `setScope re-runs the last query with the scoped library id`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val libRepo = mockk<LibraryRepository>()
        val scoped = lib("l1", "Movies")
        coEvery { libRepo.getLibraries() } returns listOf(scoped)
        coEvery { itemRepo.search("star", any(), null) } returns listOf(result("global"))
        coEvery { itemRepo.search("star", any(), "l1") } returns listOf(result("scoped"))

        val vm = SearchViewModel(itemRepo, libRepo, stubDiscover(), stubPrefs())
        advanceUntilIdle()

        vm.search("star")
        advanceUntilIdle()
        assertThat(vm.results.value.map { it.id }).containsExactly("global")

        vm.setScope(scoped)
        advanceUntilIdle()

        assertThat(vm.scope.value?.id).isEqualTo("l1")
        assertThat(vm.results.value.map { it.id }).containsExactly("scoped")
    }

    @Test
    fun `setScope before any query is a no-op on results`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val libRepo = mockk<LibraryRepository>()
        coEvery { libRepo.getLibraries() } returns emptyList()

        val vm = SearchViewModel(itemRepo, libRepo, stubDiscover(), stubPrefs())
        advanceUntilIdle()

        vm.setScope(lib("l1", "Movies"))
        advanceUntilIdle()

        assertThat(vm.results.value).isEmpty()
        coVerify(exactly = 0) { itemRepo.search(any(), any(), any()) }
    }
}
