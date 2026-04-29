package tv.onscreen.mobile.ui.search

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.model.SearchResult
import tv.onscreen.mobile.data.prefs.SearchFilters
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository

@OptIn(ExperimentalCoroutinesApi::class)
class SearchViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After  fun tearDown() { Dispatchers.resetMain() }

    private fun result(id: String, type: String) =
        SearchResult(id = id, library_id = "lib-1", title = "t-$id", type = type)

    private fun prefs(initial: SearchFilters): ServerPrefs {
        val p = mockk<ServerPrefs>(relaxed = true)
        // Filter chips read from a hot StateFlow on real prefs; the
        // mock returns a MutableStateFlow so the test can observe the
        // VM's combine() pick the latest value up.
        val flow = MutableStateFlow(initial)
        coEvery { p.searchFilters } returns flow
        coEvery { p.setSearchFilters(any()) } answers {
            flow.value = firstArg()
        }
        return p
    }

    @Test
    fun `default filters mask episode and track results`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.search(any(), any(), any()) } returns listOf(
            result("m", "movie"),
            result("e", "episode"),
            result("t", "track"),
            result("s", "show"),
        )
        val vm = SearchViewModel(repo, prefs(SearchFilters(movie = true, show = true, episode = false, track = false)))

        vm.onQueryChange("hello")
        advanceTimeBy(400)
        advanceUntilIdle()

        assertThat(vm.state.value.results.map { it.id }).containsExactly("m", "s")
    }

    @Test
    fun `track chip surfaces tracks albums and artists`() = runTest(dispatcher) {
        // Album + artist piggyback on the track filter — keeps the
        // chip count to four while still letting users hide all-music
        // results in one toggle.
        val repo = mockk<ItemRepository>()
        coEvery { repo.search(any(), any(), any()) } returns listOf(
            result("alb", "album"),
            result("art", "artist"),
            result("trk", "track"),
            result("ep", "episode"),
        )
        val vm = SearchViewModel(repo, prefs(SearchFilters(movie = false, show = false, episode = false, track = true)))

        vm.onQueryChange("led zeppelin")
        advanceTimeBy(400)
        advanceUntilIdle()

        assertThat(vm.state.value.results.map { it.id })
            .containsExactly("alb", "art", "trk").inOrder()
    }

    @Test
    fun `toggling a chip re-filters results without re-querying`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.search(any(), any(), any()) } returns listOf(
            result("m", "movie"),
            result("e", "episode"),
        )
        val sp = prefs(SearchFilters(movie = true, show = true, episode = false, track = false))
        val vm = SearchViewModel(repo, sp)

        vm.onQueryChange("query")
        advanceTimeBy(400)
        advanceUntilIdle()
        assertThat(vm.state.value.results.map { it.id }).containsExactly("m")

        // Now turn episodes on. Re-running the query is wasted work —
        // the visible row is just a filter view of the cached results.
        vm.toggleFilter(SearchViewModel.FilterType.EPISODE)
        advanceUntilIdle()

        assertThat(vm.state.value.results.map { it.id }).containsExactlyElementsIn(listOf("m", "e"))
    }

    @Test
    fun `query under 2 chars clears results`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>(relaxed = true)
        val vm = SearchViewModel(repo, prefs(SearchFilters(movie = true, show = true, episode = false, track = false)))

        vm.onQueryChange("a")
        advanceUntilIdle()

        assertThat(vm.state.value.results).isEmpty()
        assertThat(vm.state.value.loading).isFalse()
    }
}
