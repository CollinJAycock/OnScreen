package tv.onscreen.android.ui.browse

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.android.data.model.MediaItem
import tv.onscreen.android.data.model.RandomLibraryItem
import tv.onscreen.android.data.repository.LibraryRepository

@OptIn(ExperimentalCoroutinesApi::class)
class LibraryViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() { Dispatchers.setMain(dispatcher) }

    @After
    fun tearDown() { Dispatchers.resetMain() }

    private fun item(id: String) = MediaItem(
        id = id, title = "t-$id", type = "movie",
        created_at = "2026-01-01T00:00:00Z", updated_at = "2026-01-01T00:00:00Z",
    )

    @Test
    fun `load fetches first page and genres`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } returns
            (listOf(item("a"), item("b")) to 2)
        coEvery { repo.getGenres("lib") } returns listOf("Action", "Drama")

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        assertThat(vm.items.value.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(vm.genres.value).containsExactly("Action", "Drama").inOrder()
        assertThat(vm.error.value).isNull()
    }

    @Test
    fun `load applies per-type default sort for home_video`() = runTest(dispatcher) {
        // home_video / photo / dvr libraries default to "Recently
        // added" (created_at DESC) instead of title-ASC. Locks the
        // Path-A library-screen home_video specialization.
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("hv", 50, 0, "created_at", "desc", null) } returns
            (listOf(item("clip-1")) to 1)
        coEvery { repo.getGenres("hv") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("hv", "home_video")
        advanceUntilIdle()

        assertThat(vm.sort.value.sort).isEqualTo("created_at")
        assertThat(vm.sort.value.sortDir).isEqualTo("desc")
        assertThat(vm.items.value).hasSize(1)
    }

    @Test
    fun `load keeps title-asc default for movie libraries`() = runTest(dispatcher) {
        // Belt-and-braces — make sure the home_video branch doesn't
        // accidentally bleed into other library types.
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("mov", 50, 0, "title", "asc", null) } returns
            (listOf(item("a")) to 1)
        coEvery { repo.getGenres("mov") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("mov", "movie")
        advanceUntilIdle()

        assertThat(vm.sort.value.sort).isEqualTo("title")
        assertThat(vm.sort.value.sortDir).isEqualTo("asc")
    }

    @Test
    fun `loadMore appends pages and stops at total`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } returns
            (listOf(item("a"), item("b")) to 3)
        coEvery { repo.getItems("lib", 50, 2, "title", "asc", null) } returns
            (listOf(item("c")) to 3)
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        vm.loadMore()
        advanceUntilIdle()

        assertThat(vm.items.value.map { it.id }).containsExactly("a", "b", "c").inOrder()

        // Further loadMore past total should not hit the repo.
        vm.loadMore()
        advanceUntilIdle()
        coVerify(exactly = 1) { repo.getItems("lib", 50, 2, "title", "asc", null) }
    }

    @Test
    fun `setSort resets list and reloads with new sort params`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } returns
            (listOf(item("a"), item("b")) to 2)
        coEvery { repo.getItems("lib", 50, 0, "year", "desc", null) } returns
            (listOf(item("c")) to 1)
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        vm.setSort("year", "desc")
        advanceUntilIdle()

        assertThat(vm.items.value.map { it.id }).containsExactly("c")
        assertThat(vm.sort.value.sort).isEqualTo("year")
        assertThat(vm.sort.value.sortDir).isEqualTo("desc")
    }

    @Test
    fun `setSort with identical params is a no-op`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } returns
            (listOf(item("a")) to 1)
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        vm.setSort("title", "asc")
        advanceUntilIdle()

        coVerify(exactly = 1) { repo.getItems("lib", 50, 0, "title", "asc", null) }
    }

    @Test
    fun `setGenre resets list and reloads with the genre filter`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } returns
            (listOf(item("a"), item("b")) to 2)
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", "Action") } returns
            (listOf(item("c")) to 1)
        coEvery { repo.getGenres("lib") } returns listOf("Action")

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        vm.setGenre("Action")
        advanceUntilIdle()

        assertThat(vm.items.value.map { it.id }).containsExactly("c")
        assertThat(vm.genre.value).isEqualTo("Action")
    }

    @Test
    fun `error only surfaces when items list is empty`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } throws RuntimeException("boom")
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        assertThat(vm.error.value).isEqualTo("boom")
        assertThat(vm.items.value).isEmpty()
    }

    @Test
    fun `loadMore failure after success keeps existing items and clears error`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } returns
            (listOf(item("a")) to 5)
        coEvery { repo.getItems("lib", 50, 1, "title", "asc", null) } throws RuntimeException("net")
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        vm.loadMore()
        advanceUntilIdle()

        assertThat(vm.items.value.map { it.id }).containsExactly("a")
        assertThat(vm.error.value).isNull()
    }
    // ── v2.5 Watch filter ───────────────────────────────────────────────────

    @Test
    fun `setWatchFilter resets the list and sends watch with the current sort and genre`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns
            (listOf(item("a"), item("b")) to 2)
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", "Drama", null) } returns
            (listOf(item("b")) to 1)
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", "Drama", "unwatched") } returns
            (listOf(item("u")) to 1)
        coEvery { repo.getGenres("lib") } returns listOf("Drama")

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()
        vm.setGenre("Drama")
        advanceUntilIdle()

        vm.setWatchFilter("unwatched")
        advanceUntilIdle()

        assertThat(vm.watch.value).isEqualTo("unwatched")
        assertThat(vm.items.value.map { it.id }).containsExactly("u")
        coVerify(exactly = 1) { repo.getItems("lib", 50, 0, "title", "asc", "Drama", "unwatched") }
    }

    @Test
    fun `watch filter carries into later pages and clearing it reloads unfiltered`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns
            (listOf(item("a")) to 1)
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, "in_progress") } returns
            (listOf(item("p1")) to 2)
        coEvery { repo.getItems("lib", 50, 1, "title", "asc", null, "in_progress") } returns
            (listOf(item("p2")) to 2)
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib", "show")
        advanceUntilIdle()

        vm.setWatchFilter("in_progress")
        advanceUntilIdle()
        vm.loadMore()
        advanceUntilIdle()
        assertThat(vm.items.value.map { it.id }).containsExactly("p1", "p2").inOrder()

        vm.setWatchFilter(null)
        advanceUntilIdle()
        assertThat(vm.watch.value).isNull()
        assertThat(vm.items.value.map { it.id }).containsExactly("a")
        coVerify(exactly = 2) { repo.getItems("lib", 50, 0, "title", "asc", null, null) }
    }

    @Test
    fun `an unknown or unchanged watch filter does not refetch`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns
            (listOf(item("a")) to 1)
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        vm.setWatchFilter("bogus") // parses to "all", which is already set
        vm.setWatchFilter(null)
        advanceUntilIdle()

        assertThat(vm.watch.value).isNull()
        coVerify(exactly = 1) { repo.getItems("lib", 50, 0, "title", "asc", null, null) }
    }
    // ── Surprise me ─────────────────────────────────────────────────────────

    @Test
    fun `surpriseMe picks under the current genre and watch filters`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns (listOf(item("a")) to 1)
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, "unwatched") } returns (listOf(item("a")) to 1)
        coEvery { repo.getGenres("lib") } returns emptyList()
        coEvery { repo.pickRandom("lib", null, "unwatched") } returns RandomLibraryItem("m9", "movie")

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()
        vm.setWatchFilter("unwatched")
        advanceUntilIdle()
        val events = mutableListOf<LibraryEvent>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { vm.events.collect { events.add(it) } }

        vm.surpriseMe()
        advanceUntilIdle()

        assertThat(events).containsExactly(LibraryEvent.Open("m9", "movie"))
    }

    @Test
    fun `surpriseMe reports nothing-to-pick and failures`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns (listOf(item("a")) to 1)
        coEvery { repo.getGenres("lib") } returns emptyList()
        coEvery { repo.pickRandom("lib", null, null) } returns null

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()
        val events = mutableListOf<LibraryEvent>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { vm.events.collect { events.add(it) } }

        vm.surpriseMe()
        advanceUntilIdle()
        coEvery { repo.pickRandom("lib", null, null) } throws RuntimeException("501")
        vm.surpriseMe()
        advanceUntilIdle()

        assertThat(events).containsExactly(LibraryEvent.NothingToPick, LibraryEvent.PickFailed).inOrder()
    }
    // ── refresh after a detail round-trip ───────────────────────────────────

    @Test
    fun `refreshAround re-reads the item's page and swaps changed items in place`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        val a = item("a")
        val b = item("b")
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns (listOf(a, b) to 2)
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()

        // The user marked "b" watched on its detail page.
        val bWatched = b.copy(watch_state = "watched")
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns (listOf(a, bWatched) to 2)
        vm.refreshAround("b")
        advanceUntilIdle()

        assertThat(vm.items.value).containsExactly(a, bWatched).inOrder()
    }

    @Test
    fun `refreshAround leaves membership alone and ignores failures`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, "unwatched") } returns
            (listOf(item("a"), item("b")) to 2)
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns (emptyList<MediaItem>() to 0)
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()
        vm.setWatchFilter("unwatched")
        advanceUntilIdle()

        // "b" no longer matches the filter server-side; it stays until a reload.
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, "unwatched") } returns (listOf(item("a")) to 1)
        vm.refreshAround("b")
        advanceUntilIdle()
        assertThat(vm.items.value.map { it.id }).containsExactly("a", "b").inOrder()

        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, "unwatched") } throws RuntimeException("offline")
        vm.refreshAround("a")
        advanceUntilIdle()
        assertThat(vm.items.value.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(vm.error.value).isNull()
    }
}
