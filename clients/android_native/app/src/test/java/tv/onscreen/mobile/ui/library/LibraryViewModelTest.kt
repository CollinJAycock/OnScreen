package tv.onscreen.mobile.ui.library

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
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.mobile.R
import tv.onscreen.mobile.i18n.UiText
import tv.onscreen.mobile.data.model.Library
import tv.onscreen.mobile.data.model.LibraryLetter
import tv.onscreen.mobile.data.model.MediaItem
import tv.onscreen.mobile.data.model.RandomLibraryItem
import tv.onscreen.mobile.data.model.WatchFilter
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.LibraryRepository

@OptIn(ExperimentalCoroutinesApi::class)
class LibraryViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    private fun item(id: String, type: String = "movie", watchState: String? = null) = MediaItem(
        id = id, title = id, type = type,
        created_at = "2026-01-01T00:00:00Z", updated_at = "0", watch_state = watchState,
    )

    private fun lib(type: String) = Library(
        id = "L", name = "Films", type = type,
        created_at = "2026-01-01T00:00:00Z", updated_at = "2026-01-01T00:00:00Z",
    )

    private fun http(code: Int) = HttpException(Response.error<Unit>(code, "".toResponseBody(null)))

    private val prefs = mockk<ServerPrefs>(relaxed = true)

    private fun repoWith(type: String = "movie"): LibraryRepository {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getLibraries() } returns listOf(lib(type))
        coEvery { repo.getItems("L", any(), any(), any(), any(), any(), any()) } returns (listOf(item("a")) to 1)
        return repo
    }

    @Test
    fun `load resolves the library name and type - watch controls follow the type`() = runTest(dispatcher) {
        val vm = LibraryViewModel(repoWith("movie"), mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()
        assertThat(vm.state.value.libraryName).isEqualTo("Films")
        assertThat(vm.state.value.watchControls).isTrue()

        val music = LibraryViewModel(repoWith("music"), mockk(), prefs)
        music.load("L")
        advanceUntilIdle()
        assertThat(music.state.value.watchControls).isFalse()
    }

    @Test
    fun `watch filter reloads page one with the watch param`() = runTest(dispatcher) {
        val repo = repoWith()
        coEvery { repo.getItems("L", any(), 0, any(), any(), any(), "unwatched") } returns (listOf(item("u")) to 1)
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()
        coVerify { repo.getItems("L", any(), 0, any(), any(), any(), null) }

        vm.setWatchFilter(WatchFilter.UNWATCHED)
        advanceUntilIdle()
        assertThat(vm.state.value.watchFilter).isEqualTo(WatchFilter.UNWATCHED)
        assertThat(vm.state.value.pages.loadedItems.map { it.id }).containsExactly("u")

        // Same filter again is a no-op (no extra fetch).
        vm.setWatchFilter(WatchFilter.UNWATCHED)
        advanceUntilIdle()
        coVerify(exactly = 1) { repo.getItems("L", any(), 0, any(), any(), any(), "unwatched") }
    }

    @Test
    fun `a stale page from the previous filter is dropped`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getLibraries() } returns listOf(lib("movie"))
        val slowAll = CompletableDeferred<Pair<List<MediaItem>, Int>>()
        coEvery { repo.getItems("L", any(), 0, any(), any(), any(), null) } coAnswers { slowAll.await() }
        coEvery { repo.getItems("L", any(), 0, any(), any(), any(), "watched") } returns (listOf(item("w")) to 1)
        val vm = LibraryViewModel(repo, mockk(), prefs)

        vm.load("L")
        advanceUntilIdle() // "All" fetch parked
        vm.setWatchFilter(WatchFilter.WATCHED)
        advanceUntilIdle()
        slowAll.complete(listOf(item("x"), item("y")) to 2)
        advanceUntilIdle()

        assertThat(vm.state.value.pages.loadedItems.map { it.id }).containsExactly("w")
        assertThat(vm.state.value.total).isEqualTo(1)
    }

    @Test
    fun `surprise me opens the pick with the current filter`() = runTest(dispatcher) {
        val repo = repoWith()
        coEvery { repo.randomItem("L", null, "in_progress") } returns RandomLibraryItem("r1", "movie")
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        vm.setWatchFilter(WatchFilter.IN_PROGRESS)
        advanceUntilIdle()

        vm.surpriseMe()
        assertThat(vm.state.value.surprising).isTrue()
        advanceUntilIdle()
        assertThat(vm.state.value.surprising).isFalse()
        assertThat(vm.state.value.randomPick).isEqualTo(RandomLibraryItem("r1", "movie"))

        vm.consumeRandomPick()
        assertThat(vm.state.value.randomPick).isNull()
    }

    @Test
    fun `surprise me with no match posts a message`() = runTest(dispatcher) {
        val repo = repoWith()
        coEvery { repo.randomItem("L", null, "watched") } returns null
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        vm.setWatchFilter(WatchFilter.WATCHED)
        advanceUntilIdle()

        vm.surpriseMe()
        advanceUntilIdle()
        assertThat(vm.state.value.randomPick).isNull()
        assertThat(vm.state.value.message).isEqualTo(
            UiText.res(R.string.library_surprise_no_match, UiText.res(R.string.watch_filter_watched)),
        )
    }

    @Test
    fun `surprise me on a server without the watch store says so`() = runTest(dispatcher) {
        val repo = repoWith()
        coEvery { repo.randomItem("L", null, null) } throws http(501)
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()

        vm.surpriseMe()
        advanceUntilIdle()
        assertThat(vm.state.value.message).isEqualTo(UiText.res(R.string.library_surprise_unsupported))
        assertThat(vm.state.value.surprising).isFalse()
    }

    @Test
    fun `grid mark is optimistic and rolls back on failure`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getLibraries() } returns listOf(lib("movie"))
        coEvery { repo.getItems("L", any(), any(), any(), any(), any(), any()) } returns
            (listOf(item("a", watchState = "unwatched"), item("b")) to 2)
        val items = mockk<ItemRepository>()
        coEvery { items.setWatched("a", true) } returns Unit
        coEvery { items.setWatched("b", true) } throws http(429)
        val vm = LibraryViewModel(repo, items, prefs)
        vm.load("L")
        advanceUntilIdle()

        vm.setWatched(vm.state.value.pages.loadedItems[0], true)
        assertThat(vm.state.value.pages.loadedItems[0].watch_state).isEqualTo("watched")
        advanceUntilIdle()
        assertThat(vm.state.value.pages.loadedItems[0].watch_state).isEqualTo("watched")
        assertThat(vm.state.value.message).isEqualTo(UiText.res(R.string.library_marked_watched))
        vm.consumeMessage()

        vm.setWatched(vm.state.value.pages.loadedItems[1], true)
        advanceUntilIdle()
        assertThat(vm.state.value.pages.loadedItems[1].watch_state).isNull()
        assertThat(vm.state.value.message).isEqualTo(UiText.res(R.string.watch_mark_rate_limited))
    }

    @Test
    fun `tracks cannot be marked`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getLibraries() } returns listOf(lib("music"))
        coEvery { repo.getItems("L", any(), any(), any(), any(), any(), any()) } returns (listOf(item("t", type = "track")) to 1)
        val items = mockk<ItemRepository>()
        val vm = LibraryViewModel(repo, items, prefs)
        vm.load("L")
        advanceUntilIdle()

        vm.setWatched(vm.state.value.pages.loadedItems[0], true)
        advanceUntilIdle()
        coVerify(exactly = 0) { items.setWatched(any(), any()) }
    }

    // ── Quiet refresh on resume ───────────────────────────────────────────

    @Test
    fun `coming back refreshes badges and filter membership without a spinner`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getLibraries() } returns listOf(lib("movie"))
        coEvery { repo.getItems("L", any(), any(), any(), any(), any(), null) } returns (emptyList<MediaItem>() to 0)
        coEvery { repo.getItems("L", any(), 0, any(), any(), any(), "unwatched") } returnsMany listOf(
            listOf(item("a", watchState = "unwatched"), item("b", watchState = "unwatched")) to 2,
            // "a" was watched on its detail page / in the player meanwhile.
            listOf(item("b", watchState = "in_progress")) to 1,
        )
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        vm.setWatchFilter(WatchFilter.UNWATCHED)
        advanceUntilIdle()
        assertThat(vm.state.value.pages.loadedItems.map { it.id }).containsExactly("a", "b")

        // Re-entering the screen: load() keeps the stale items (by design)…
        vm.load("L")
        advanceUntilIdle()
        assertThat(vm.state.value.pages.loadedItems.map { it.id }).containsExactly("a", "b")

        // …and the ON_RESUME quiet refresh brings them up to date.
        vm.refreshQuietly()
        assertThat(vm.state.value.loading).isFalse() // no spinner / pull indicator
        advanceUntilIdle()
        assertThat(vm.state.value.watchFilter).isEqualTo(WatchFilter.UNWATCHED)
        assertThat(vm.state.value.pages.loadedItems.map { it.id }).containsExactly("b")
        assertThat(vm.state.value.pages.loadedItems.single().watch_state).isEqualTo("in_progress")
        assertThat(vm.state.value.total).isEqualTo(1)
    }

    @Test
    fun `quiet refresh re-pulls the pages on screen so a deep scroll survives`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getLibraries() } returns listOf(lib("movie"))
        val page0 = (0 until 100).map { item("p0-$it") }
        val page1 = (0 until 100).map { item("p1-$it") }
        coEvery { repo.getItems("L", any(), 0, any(), any(), any(), null) } returns (page0 to 250)
        coEvery { repo.getItems("L", any(), 100, any(), any(), any(), null) } returns (page1 to 250)
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()
        // The grid scrolled into the second page.
        vm.onVisibleRows(150, 160)
        advanceUntilIdle()
        assertThat(vm.state.value.pages.loadedItems).hasSize(200)

        vm.refreshQuietly()
        advanceUntilIdle()
        // The page on screen again (not page one), nothing past it; the grid
        // keeps its length so the scroll position holds.
        assertThat(vm.state.value.total).isEqualTo(250)
        assertThat(vm.state.value.pages[150]!!.id).isEqualTo("p1-50")
        coVerify(exactly = 2) { repo.getItems("L", any(), 100, any(), any(), any(), null) }
        coVerify(exactly = 1) { repo.getItems("L", any(), 0, any(), any(), any(), null) }
        coVerify(exactly = 0) { repo.getItems("L", any(), 200, any(), any(), any(), null) }

        // The page off screen went back to a placeholder and reloads, fresh,
        // when the grid scrolls back to it.
        assertThat(vm.state.value.pages[50]).isNull()
        vm.onVisibleRows(40, 50)
        advanceUntilIdle()
        assertThat(vm.state.value.pages[50]!!.id).isEqualTo("p0-50")
        coVerify(exactly = 2) { repo.getItems("L", any(), 0, any(), any(), any(), null) }
    }

    @Test
    fun `quiet refresh keeps the grid on failure and never runs before the first load`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getLibraries() } returns listOf(lib("movie"))
        coEvery { repo.getItems("L", any(), any(), any(), any(), any(), any()) } returns (listOf(item("a")) to 1)
        val vm = LibraryViewModel(repo, mockk(), prefs)

        vm.refreshQuietly() // first ON_RESUME, before load()
        advanceUntilIdle()
        coVerify(exactly = 0) { repo.getItems(any(), any(), any(), any(), any(), any(), any()) }

        vm.load("L")
        advanceUntilIdle()
        coEvery { repo.getItems("L", any(), any(), any(), any(), any(), any()) } throws http(502)
        vm.refreshQuietly()
        advanceUntilIdle()
        assertThat(vm.state.value.pages.loadedItems.map { it.id }).containsExactly("a")
        assertThat(vm.state.value.error).isNull()
    }

    @Test
    fun `a filter change during a quiet refresh wins`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getLibraries() } returns listOf(lib("movie"))
        val slow = CompletableDeferred<Pair<List<MediaItem>, Int>>()
        var calls = 0
        coEvery { repo.getItems("L", any(), 0, any(), any(), any(), null) } coAnswers {
            if (calls++ == 0) listOf(item("a")) to 1 else slow.await()
        }
        coEvery { repo.getItems("L", any(), 0, any(), any(), any(), "watched") } returns (listOf(item("w")) to 1)
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()

        vm.refreshQuietly()
        advanceUntilIdle() // parked
        vm.setWatchFilter(WatchFilter.WATCHED)
        advanceUntilIdle()
        slow.complete(listOf(item("x")) to 1)
        advanceUntilIdle()
        assertThat(vm.state.value.pages.loadedItems.map { it.id }).containsExactly("w")
    }

    @Test
    fun `a filter change bumps the query version - refreshes do not`() = runTest(dispatcher) {
        val repo = repoWith()
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()
        val start = vm.state.value.queryVersion

        // Same query re-listed: the grid keeps its scroll position.
        vm.refreshQuietly()
        advanceUntilIdle()
        vm.refresh()
        advanceUntilIdle()
        vm.load("L")
        advanceUntilIdle()
        assertThat(vm.state.value.queryVersion).isEqualTo(start)

        // New query: the screen scrolls back to the top.
        vm.setWatchFilter(WatchFilter.IN_PROGRESS)
        assertThat(vm.state.value.queryVersion).isEqualTo(start + 1)
        vm.setWatchFilter(WatchFilter.IN_PROGRESS) // no-op re-select
        assertThat(vm.state.value.queryVersion).isEqualTo(start + 1)
        vm.setWatchFilter(WatchFilter.ALL)
        advanceUntilIdle()
        assertThat(vm.state.value.queryVersion).isEqualTo(start + 2)
    }

    // ── A-Z index + jumping ───────────────────────────────────────────────

    /** A [total]-row listing where row n is item "r<n>", served a page at a
     *  time like the server's limit/offset. */
    private fun bigRepo(total: Int = 5000): LibraryRepository {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getLibraries() } returns listOf(lib("movie"))
        coEvery { repo.getItems("L", any(), any(), any(), any(), any(), any()) } coAnswers {
            val limit = secondArg<Int>()
            val offset = thirdArg<Int>()
            (offset until minOf(offset + limit, total)).map { item("r$it") } to total
        }
        coEvery { repo.getLetters("L", any(), any(), any(), any()) } returns listOf(
            LibraryLetter("#", 12, 0),
            LibraryLetter("A", 400, 12),
            LibraryLetter("M", 300, 4350),
        )
        return repo
    }

    @Test
    fun `load fetches the letter index with the listing's parameters`() = runTest(dispatcher) {
        val repo = bigRepo()
        coEvery { repo.getLetters("L", any(), any(), any(), "unwatched") } returns listOf(LibraryLetter("B", 3, 0))
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()
        assertThat(vm.state.value.letters.map { it.letter }).containsExactly("#", "A", "M").inOrder()
        coVerify { repo.getLetters("L", null, null, null, null) }

        // A new filter is a new listing: its own index, same ?watch= as /items.
        vm.setWatchFilter(WatchFilter.UNWATCHED)
        assertThat(vm.state.value.letters).isEmpty() // old offsets never apply to it
        advanceUntilIdle()
        assertThat(vm.state.value.letters).containsExactly(LibraryLetter("B", 3, 0))
    }

    @Test
    fun `no rail when the server sends an empty index - a non-title sort`() = runTest(dispatcher) {
        // The server answers [] unless the listing is sorted by title.
        val repo = bigRepo()
        coEvery { repo.getLetters("L", any(), any(), any(), any()) } returns emptyList()
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()
        assertThat(vm.state.value.letters).isEmpty()
        assertThat(vm.state.value.total).isEqualTo(5000) // the grid itself is unaffected
    }

    @Test
    fun `no rail on a server without the endpoint - 404 - or when it fails`() = runTest(dispatcher) {
        // 404 → empty in the repository (see LibraryRepositoryLettersTest);
        // anything else it throws is swallowed here.
        val repo = bigRepo()
        coEvery { repo.getLetters("L", any(), any(), any(), any()) } throws http(500)
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()
        assertThat(vm.state.value.letters).isEmpty()
        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.total).isEqualTo(5000)
    }

    @Test
    fun `the grid is the whole listing from page one - unloaded rows are null`() = runTest(dispatcher) {
        val vm = LibraryViewModel(bigRepo(), mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()
        val pages = vm.state.value.pages
        assertThat(pages.total).isEqualTo(5000)
        assertThat(pages[0]!!.id).isEqualTo("r0")
        assertThat(pages[99]!!.id).isEqualTo("r99")
        assertThat(pages[100]).isNull()
        assertThat(pages[4999]).isNull()
    }

    @Test
    fun `jumping to a letter loads the page at its offset and nothing before it`() = runTest(dispatcher) {
        val repo = bigRepo()
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()
        vm.onVisibleRows(0, 11)
        advanceUntilIdle()

        vm.jumpTo(LibraryLetter("M", 300, 4350))
        advanceUntilIdle()

        // Row 4350 lives in page 43 (offset 4300); the 30-row margin above
        // it stays inside that page.
        coVerify(exactly = 1) { repo.getItems("L", 100, 4300, any(), any(), any(), null) }
        assertThat(vm.state.value.pages[4350]!!.id).isEqualTo("r4350")
        // Pages 1..42 were never requested.
        for (offset in 100..4200 step 100) {
            coVerify(exactly = 0) { repo.getItems("L", any(), offset, any(), any(), any(), any()) }
        }
    }

    @Test
    fun `scrolling up from a jump loads the earlier page`() = runTest(dispatcher) {
        val repo = bigRepo()
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()
        vm.jumpTo(LibraryLetter("M", 300, 4350))
        advanceUntilIdle()
        assertThat(vm.state.value.pages[4290]).isNull()

        vm.onVisibleRows(4285, 4296)
        advanceUntilIdle()
        coVerify(exactly = 1) { repo.getItems("L", 100, 4200, any(), any(), any(), null) }
        assertThat(vm.state.value.pages[4290]!!.id).isEqualTo("r4290")
    }

    @Test
    fun `a page the grid has left is cancelled and never lands`() = runTest(dispatcher) {
        val repo = bigRepo()
        val parked = CompletableDeferred<Pair<List<MediaItem>, Int>>()
        coEvery { repo.getItems("L", any(), 4300, any(), any(), any(), any()) } coAnswers { parked.await() }
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()

        vm.jumpTo(LibraryLetter("M", 300, 4350))
        advanceUntilIdle() // page 43 in flight
        vm.jumpTo(LibraryLetter("A", 400, 12))
        advanceUntilIdle()
        parked.complete(listOf(item("late")) to 5000)
        advanceUntilIdle()

        assertThat(vm.state.value.pages.isLoaded(43)).isFalse()
        assertThat(vm.state.value.pages[12]!!.id).isEqualTo("r12")
    }

    @Test
    fun `a page from the previous filter is dropped`() = runTest(dispatcher) {
        val repo = bigRepo()
        val parked = CompletableDeferred<Pair<List<MediaItem>, Int>>()
        coEvery { repo.getItems("L", any(), 4300, any(), any(), any(), null) } coAnswers { parked.await() }
        coEvery { repo.getItems("L", any(), any(), any(), any(), any(), "watched") } returns (listOf(item("w")) to 1)
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()
        vm.jumpTo(LibraryLetter("M", 300, 4350))
        advanceUntilIdle()

        vm.setWatchFilter(WatchFilter.WATCHED)
        advanceUntilIdle()
        parked.complete(listOf(item("late")) to 5000)
        advanceUntilIdle()

        assertThat(vm.state.value.total).isEqualTo(1)
        assertThat(vm.state.value.pages.loadedItems.map { it.id }).containsExactly("w")
    }

    @Test
    fun `a failed page is retried while it stays on screen`() = runTest(dispatcher) {
        val repo = bigRepo()
        var calls = 0
        coEvery { repo.getItems("L", any(), 4300, any(), any(), any(), any()) } coAnswers {
            if (calls++ == 0) throw http(502)
            (4300 until 4400).map { item("r$it") } to 5000
        }
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()
        vm.jumpTo(LibraryLetter("M", 300, 4350))
        advanceUntilIdle()

        assertThat(calls).isEqualTo(2)
        assertThat(vm.state.value.pages[4350]!!.id).isEqualTo("r4350")
    }

    @Test
    fun `marking a jumped-to card updates it in place`() = runTest(dispatcher) {
        val repo = bigRepo()
        val items = mockk<ItemRepository>()
        coEvery { items.setWatched("r4350", true) } returns Unit
        val vm = LibraryViewModel(repo, items, prefs)
        vm.load("L")
        advanceUntilIdle()
        vm.jumpTo(LibraryLetter("M", 300, 4350))
        advanceUntilIdle()

        vm.setWatched(vm.state.value.pages[4350]!!, true)
        advanceUntilIdle()
        assertThat(vm.state.value.pages[4350]!!.watch_state).isEqualTo("watched")
    }

    @Test
    fun `quiet refresh re-pulls jumped-to pages and the letter index`() = runTest(dispatcher) {
        val repo = bigRepo()
        val vm = LibraryViewModel(repo, mockk(), prefs)
        vm.load("L")
        advanceUntilIdle()
        vm.jumpTo(LibraryLetter("M", 300, 4350))
        advanceUntilIdle()

        vm.refreshQuietly()
        advanceUntilIdle()
        coVerify(exactly = 2) { repo.getItems("L", any(), 4300, any(), any(), any(), null) }
        coVerify(exactly = 2) { repo.getLetters("L", any(), any(), any(), null) }
        assertThat(vm.state.value.pages[4350]!!.id).isEqualTo("r4350")
        // Page one is off screen: back to placeholders, not re-pulled.
        coVerify(exactly = 1) { repo.getItems("L", any(), 0, any(), any(), any(), null) }
        assertThat(vm.state.value.pages.isLoaded(0)).isFalse()
    }
}
