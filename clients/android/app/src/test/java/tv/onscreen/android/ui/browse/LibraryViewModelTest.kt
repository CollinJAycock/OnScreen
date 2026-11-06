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
import tv.onscreen.android.data.model.LibraryLetter
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
    // ── A–Z jump index ──────────────────────────────────────────────────────

    private fun page(from: Int, size: Int = 50) = (from until from + size).map { item("i$it") }

    @Test
    fun `letters load for the title sort with the listing's filters`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns (page(0) to 300)
        coEvery { repo.getGenres("lib") } returns emptyList()
        coEvery { repo.getLetters("lib", "title", "asc", null, null) } returns
            listOf(LibraryLetter("#", 3, 0), LibraryLetter("A", 40, 3))

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()

        assertThat(vm.letters.value.map { it.letter }).containsExactly("#", "A").inOrder()
    }

    @Test
    fun `letters are hidden and not fetched for a non-title sort`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, any(), any(), null, null) } returns (page(0) to 300)
        coEvery { repo.getGenres("lib") } returns emptyList()
        coEvery { repo.getLetters("lib", "title", "asc", null, null) } returns listOf(LibraryLetter("A", 1, 0))

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()
        assertThat(vm.letters.value).isNotEmpty()

        vm.setSort("year", "desc")
        advanceUntilIdle()

        assertThat(vm.letters.value).isEmpty()
        coVerify(exactly = 0) { repo.getLetters("lib", "year", any(), any(), any()) }
    }

    @Test
    fun `letters stay hidden on an older server and are not asked for again`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", any(), any(), null) } returns (page(0) to 300)
        coEvery { repo.getGenres("lib") } returns listOf("Drama")
        coEvery { repo.getLetters(any(), any(), any(), any(), any()) } returns null // 404

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()
        vm.setGenre("Drama")
        advanceUntilIdle()

        assertThat(vm.letters.value).isEmpty()
        coVerify(exactly = 1) { repo.getLetters(any(), any(), any(), any(), any()) }
    }

    @Test
    fun `a failed letters fetch hides the rail without failing the grid`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns (page(0) to 300)
        coEvery { repo.getGenres("lib") } returns emptyList()
        coEvery { repo.getLetters(any(), any(), any(), any(), any()) } throws RuntimeException("503")

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()

        assertThat(vm.letters.value).isEmpty()
        assertThat(vm.error.value).isNull()
        assertThat(vm.items.value).hasSize(50)
    }

    @Test
    fun `letters from a superseded filter are dropped`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", any(), null) } returns (page(0) to 300)
        coEvery { repo.getGenres("lib") } returns listOf("Drama")
        coEvery { repo.getLetters("lib", "title", "asc", null, null) } coAnswers {
            kotlinx.coroutines.delay(1_000)
            listOf(LibraryLetter("Z", 1, 0)) // the unfiltered index, arriving late
        }
        coEvery { repo.getLetters("lib", "title", "asc", "Drama", null) } returns listOf(LibraryLetter("D", 1, 0))

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        vm.setGenre("Drama")
        advanceUntilIdle()

        assertThat(vm.letters.value.map { it.letter }).containsExactly("D")
    }

    @Test
    fun `jump loads only the page holding the letter and focuses its first item`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns (page(0) to 300)
        coEvery { repo.getItems("lib", 50, 200, "title", "asc", null, null) } returns (page(200) to 300)
        coEvery { repo.getGenres("lib") } returns emptyList()
        coEvery { repo.getLetters(any(), any(), any(), any(), any()) } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()

        // "S" starts at listing row 213: the page at 200 is fetched, 50..199 never are.
        assertThat(vm.jumpTo(LibraryLetter("S", 20, 213))).isNull()
        advanceUntilIdle()

        assertThat(vm.items.value.first().id).isEqualTo("i200")
        assertThat(vm.takeFocusTarget()).isEqualTo(13)
        assertThat(vm.items.value[13].id).isEqualTo("i213")
        assertThat(vm.takeFocusTarget()).isNull() // once
        assertThat(vm.listingPosition(13)).isEqualTo(213)
        coVerify(exactly = 0) { repo.getItems("lib", 50, 50, any(), any(), any(), any()) }
    }

    @Test
    fun `after a jump the grid pages both ways around the window`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns (page(0) to 300)
        coEvery { repo.getItems("lib", 50, 200, "title", "asc", null, null) } returns (page(200) to 300)
        coEvery { repo.getItems("lib", 50, 150, "title", "asc", null, null) } returns (page(150) to 300)
        coEvery { repo.getItems("lib", 50, 250, "title", "asc", null, null) } returns (page(250) to 300)
        coEvery { repo.getGenres("lib") } returns emptyList()
        coEvery { repo.getLetters(any(), any(), any(), any(), any()) } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()
        vm.jumpTo(LibraryLetter("S", 20, 213))
        advanceUntilIdle()

        vm.loadPrevious()
        advanceUntilIdle()
        assertThat(vm.items.value.first().id).isEqualTo("i150")
        assertThat(vm.listingPosition(0)).isEqualTo(150)

        vm.loadMore()
        advanceUntilIdle()
        assertThat(vm.items.value.map { it.id }).isEqualTo(page(150, 150).map { it.id })
        // At the end of the listing: no more forward pages.
        vm.loadMore()
        advanceUntilIdle()
        coVerify(exactly = 1) { repo.getItems("lib", 50, 250, any(), any(), any(), any()) }
    }

    @Test
    fun `jump to a letter already loaded focuses it without a fetch`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns (page(0) to 300)
        coEvery { repo.getGenres("lib") } returns emptyList()
        coEvery { repo.getLetters(any(), any(), any(), any(), any()) } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()

        assertThat(vm.jumpTo(LibraryLetter("B", 5, 30))).isEqualTo(30)
        advanceUntilIdle()
        coVerify(exactly = 1) { repo.getItems(any(), any(), any(), any(), any(), any(), any()) }
    }

    @Test
    fun `a failed jump keeps the grid and reports it`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns (page(0) to 300)
        coEvery { repo.getItems("lib", 50, 200, "title", "asc", null, null) } throws RuntimeException("net")
        coEvery { repo.getGenres("lib") } returns emptyList()
        coEvery { repo.getLetters(any(), any(), any(), any(), any()) } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()
        val events = mutableListOf<LibraryEvent>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { vm.events.collect { events.add(it) } }

        vm.jumpTo(LibraryLetter("S", 20, 213))
        advanceUntilIdle()

        assertThat(events).containsExactly(LibraryEvent.JumpFailed)
        assertThat(vm.items.value.first().id).isEqualTo("i0")
        assertThat(vm.takeFocusTarget()).isNull()
    }

    @Test
    fun `refreshAround after a jump re-reads the item's listing page`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null, null) } returns (page(0) to 300)
        coEvery { repo.getItems("lib", 50, 200, "title", "asc", null, null) } returns (page(200) to 300)
        coEvery { repo.getGenres("lib") } returns emptyList()
        coEvery { repo.getLetters(any(), any(), any(), any(), any()) } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib", "movie")
        advanceUntilIdle()
        vm.jumpTo(LibraryLetter("S", 20, 213))
        advanceUntilIdle()

        vm.refreshAround("i213")
        advanceUntilIdle()
        coVerify(exactly = 2) { repo.getItems("lib", 50, 200, "title", "asc", null, null) }
    }

    @Test
    fun `rail index finds the letter of a listing row in either direction`() {
        val asc = listOf(LibraryLetter("#", 2, 0), LibraryLetter("A", 10, 2), LibraryLetter("C", 5, 12))
        assertThat(LetterRail.indexFor(asc, 0)).isEqualTo(0)
        assertThat(LetterRail.indexFor(asc, 11)).isEqualTo(1)
        assertThat(LetterRail.indexFor(asc, 12)).isEqualTo(2)
        assertThat(LetterRail.indexFor(asc, 500)).isEqualTo(2)
        val desc = listOf(LibraryLetter("Z", 4, 0), LibraryLetter("M", 6, 4))
        assertThat(LetterRail.indexFor(desc, 5)).isEqualTo(1)
        assertThat(LetterRail.indexFor(emptyList(), 3)).isEqualTo(0)
    }
}
