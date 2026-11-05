package tv.onscreen.mobile.ui.search

import com.google.common.truth.Truth.assertThat
import androidx.lifecycle.SavedStateHandle
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.withContext
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
        val vm = SearchViewModel(repo, prefs(SearchFilters(movie = true, show = true, episode = false, track = false)), SavedStateHandle())

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
        val vm = SearchViewModel(repo, prefs(SearchFilters(movie = false, show = false, episode = false, track = true)), SavedStateHandle())

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
        val vm = SearchViewModel(repo, sp, SavedStateHandle())

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
        val vm = SearchViewModel(repo, prefs(SearchFilters(movie = true, show = true, episode = false, track = false)), SavedStateHandle())

        vm.onQueryChange("a")
        advanceUntilIdle()

        assertThat(vm.state.value.results).isEmpty()
        assertThat(vm.state.value.loading).isFalse()
    }

    // ── Search this library ───────────────────────────────────────────────

    private val defaults = SearchFilters(movie = true, show = true, episode = false, track = false)

    private fun scoped(id: String = "lib-9", name: String? = "Films") =
        SavedStateHandle(mapOf("libraryId" to id, "libraryName" to name))

    @Test
    fun `a library search sends library_id`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.search(any(), any(), any()) } returns listOf(result("m", "movie"))
        val vm = SearchViewModel(repo, prefs(defaults), scoped())

        vm.onQueryChange("dune")
        advanceTimeBy(400)
        advanceUntilIdle()

        coVerify(exactly = 1) { repo.search("dune", any(), "lib-9") }
        assertThat(vm.state.value.libraryScoped).isTrue()
        assertThat(vm.state.value.title).isEqualTo("Search Films")
    }

    @Test
    fun `the global search sends no library_id`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.search(any(), any(), any()) } returns emptyList()
        val vm = SearchViewModel(repo, prefs(defaults), SavedStateHandle())

        vm.onQueryChange("dune")
        advanceTimeBy(400)
        advanceUntilIdle()

        coVerify(exactly = 1) { repo.search("dune", any(), null) }
        assertThat(vm.state.value.libraryScoped).isFalse()
        assertThat(vm.state.value.title).isEqualTo("Search")
    }

    @Test
    fun `a library search shows every type it matched - the chips don't apply`() = runTest(dispatcher) {
        // In a music library the default chips (music off) would hide every
        // result; the scoped screen has no chips, so nothing is filtered.
        val repo = mockk<ItemRepository>()
        coEvery { repo.search(any(), any(), any()) } returns listOf(
            result("art", "artist"),
            result("trk", "track"),
            result("ep", "episode"),
        )
        val vm = SearchViewModel(repo, prefs(defaults), scoped(name = null))

        vm.onQueryChange("abba")
        advanceTimeBy(400)
        advanceUntilIdle()

        assertThat(vm.state.value.results.map { it.id }).containsExactly("art", "trk", "ep").inOrder()
        assertThat(vm.state.value.title).isEqualTo("Search this library")
    }

    @Test
    fun `typing waits out the debounce - one request for the final text`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.search(any(), any(), any()) } returns emptyList()
        val vm = SearchViewModel(repo, prefs(defaults), scoped())

        vm.onQueryChange("du")
        advanceTimeBy(100)
        vm.onQueryChange("dun")
        advanceTimeBy(100)
        vm.onQueryChange("dune")
        advanceTimeBy(400)
        advanceUntilIdle()

        coVerify(exactly = 1) { repo.search(any(), any(), any()) }
        coVerify(exactly = 1) { repo.search("dune", any(), "lib-9") }
    }

    @Test
    fun `a slow response for an older query is dropped`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        val slow = CompletableDeferred<List<SearchResult>>()
        // The older call ignores cancellation (as a call that has already
        // returned would); its results must still not land.
        coEvery { repo.search("star", any(), any()) } coAnswers {
            withContext(NonCancellable) { slow.await() }
        }
        coEvery { repo.search("star wars", any(), any()) } returns listOf(result("sw", "movie"))
        val vm = SearchViewModel(repo, prefs(defaults), scoped())

        vm.onQueryChange("star")
        advanceTimeBy(400) // "star" in flight
        vm.onQueryChange("star wars")
        advanceTimeBy(400)
        advanceUntilIdle()
        assertThat(vm.state.value.results.map { it.id }).containsExactly("sw")

        slow.complete(listOf(result("old", "movie")))
        advanceUntilIdle()
        assertThat(vm.state.value.results.map { it.id }).containsExactly("sw")
        assertThat(vm.state.value.query).isEqualTo("star wars")
        assertThat(vm.state.value.error).isNull()
    }

    @Test
    fun `clearing the field while a search is in flight keeps it empty`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        val slow = CompletableDeferred<List<SearchResult>>()
        coEvery { repo.search(any(), any(), any()) } coAnswers {
            withContext(NonCancellable) { slow.await() }
        }
        val vm = SearchViewModel(repo, prefs(defaults), scoped())

        vm.onQueryChange("dune")
        advanceTimeBy(400)
        vm.onQueryChange("")
        slow.complete(listOf(result("d", "movie")))
        advanceUntilIdle()

        assertThat(vm.state.value.results).isEmpty()
        assertThat(vm.state.value.loading).isFalse()
        assertThat(vm.state.value.error).isNull()
    }

    @Test
    fun `a library search with no match is an empty result, not an error`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.search(any(), any(), any()) } returns emptyList()
        val vm = SearchViewModel(repo, prefs(defaults), scoped())

        vm.onQueryChange("zzzz")
        advanceTimeBy(400)
        advanceUntilIdle()

        assertThat(vm.state.value.results).isEmpty()
        assertThat(vm.state.value.loading).isFalse()
        assertThat(vm.state.value.error).isNull()
    }
}
